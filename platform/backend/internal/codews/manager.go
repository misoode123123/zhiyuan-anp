// Package codews 管理 AI 编码工具的交互式工作台（opencode / Claude Code / Codex ...）。
// 不造轮子：复用各工具自带的 web/headless 服务，开发者浏览器访问即得原生编码体验。
//
// 工作模型：为每个应用启动一个工具实例（cwd=应用 repo），监听 0.0.0.0:<port>；
// compose 把 9400-9450 映射到宿主；开发者访问 http://<host>:<port> 即该工具的官方界面，
// 编码产出 commit 到 repo，无缝衔接 ANP 的版本/发布流程。
package codews

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const (
	portMin     = 9400
	portMax     = 9450
	defaultTool = "opencode"
)

// Manager 管理各应用的编码工作台进程（可插拔多工具）。
type Manager struct {
	host     string
	mu       sync.Mutex
	sessions map[string]*Session // appID -> 当前活跃工作台
	tools    map[string]Tool
}

// Session 一个开发者在一个应用上的编码工作台实例（per user × app × tool）。
type Session struct {
	AppID   string    `json:"app_id"`
	UserID  string    `json:"user_id"` // 开发者（不同开发者可各开各的工作台/工具）
	Tool    string    `json:"tool"`
	Port    int       `json:"port"`
	URL     string    `json:"url"`
	RepoDir string    `json:"-"`
	// SessionID 预创建的会话(带项目上下文); 开发者打开 web UI 即见此会话而非空白。
	// 空=未预创建或失败(非致命, 用户可手动新建)。
	SessionID string `json:"session_id,omitempty"`
	// DeepURL 直达预创建会话的深链接(/<base64url(repoDir)>/session/<id>);
	// 前端优先打开它, 省去用户在根路径手点会话。空=未预创建, 回退用 URL。
	DeepURL string `json:"deep_url,omitempty"`
	cmd     *exec.Cmd
	started time.Time
}

// NewManager 构造，预注册 opencode（已接入）+ claude/codex（接口预留）。
func NewManager(host string) *Manager {
	m := &Manager{host: host, sessions: map[string]*Session{}, tools: map[string]Tool{}}
	m.Register(OpenCodeTool{})
	m.Register(ClaudeTool{})
	m.Register(CodexTool{})
	return m
}

// Register 注册一个编码工具（可插拔）。
func (m *Manager) Register(t Tool) {
	m.mu.Lock()
	m.tools[t.Name()] = t
	m.mu.Unlock()
}

// Tools 已注册的工具名列表。
func (m *Manager) Tools() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.tools))
	for n := range m.tools {
		out = append(out, n)
	}
	return out
}

// Ensure 启动或复用某开发者在某应用的编码工作台。toolName 空=默认 opencode；
// 同一开发者切换工具会停旧起新；不同开发者各自独立工作台（可不同工具）。
func (m *Manager) Ensure(appID, repoDir, userID, toolName string) (*Session, error) {
	if toolName == "" {
		toolName = defaultTool
	}
	if userID == "" {
		userID = "anonymous"
	}
	key := appID + ":" + userID
	m.mu.Lock()
	tool, ok := m.tools[toolName]
	if !ok {
		m.mu.Unlock()
		return nil, fmt.Errorf("未知编码工具: %s（已注册: %v）", toolName, m.Tools())
	}
	// 同开发者同工具活跃会话 → 复用
	if s, exists := m.sessions[key]; exists && s.alive() && s.Tool == toolName {
		m.mu.Unlock()
		return s, nil
	}
	// 同开发者换工具 → 停旧起新
	if old, exists := m.sessions[key]; exists && old.cmd != nil && old.cmd.Process != nil {
		_ = old.cmd.Process.Kill()
		delete(m.sessions, key)
	}
	port := m.allocPortLocked()
	if port == 0 {
		m.mu.Unlock()
		return nil, fmt.Errorf("无可用工作台端口(%d-%d)", portMin, portMax)
	}
	m.mu.Unlock()

	cmd, err := tool.Start(repoDir, port)
	if err != nil {
		return nil, err
	}
	s := &Session{
		AppID: appID, UserID: userID, Tool: toolName, Port: port, RepoDir: repoDir, cmd: cmd, started: time.Now(),
		URL: fmt.Sprintf("http://%s:%d", m.host, port),
	}
	m.mu.Lock()
	m.sessions[key] = s
	m.mu.Unlock()

	// 进程退出时清理（端口回收）
	go func() {
		_ = cmd.Wait()
		m.mu.Lock()
		if cur, ok := m.sessions[key]; ok && cur == s {
			delete(m.sessions, key)
		}
		m.mu.Unlock()
	}()

	if !waitListen(port, 6*time.Second) {
		return nil, fmt.Errorf("%s 工作台启动后未监听 :%d", toolName, port)
	}
	// 复用 opencode 已有会话(按 repo 目录匹配,取最近);无则预创建一个带项目上下文的会话。
	// opencode 会话持久化在磁盘,进程/后端重启后据此恢复开发者上次的编码上下文,不再每次新建。失败非致命。
	s.SessionID = ensureSession(port, repoDir)
	if s.SessionID != "" {
		s.DeepURL = sessionDeepURL(s.URL, repoDir, s.SessionID)
	}
	return s, nil
}

// sessionDeepURL 生成直达 opencode 预创建会话的深链接: /<base64url(repoDir)>/session/<sessionID>。
// slug 算法与 opencode web UI 的 bn(worktree) 一致(base64url 无 padding), 使前端打开即
// 进入带项目上下文的会话, 而非根路径的空白/新建界面。
func sessionDeepURL(baseURL, repoDir, sessionID string) string {
	slug := strings.TrimRight(base64.URLEncoding.EncodeToString([]byte(repoDir)), "=")
	return fmt.Sprintf("%s/%s/session/%s", baseURL, slug, sessionID)
}

// wsHTTPClient 调工作台内置 API 的客户端(带超时, 防卡死)。
var wsHTTPClient = &http.Client{Timeout: 3 * time.Second}

// ensureSession 复用 opencode 已有会话(按 repo 目录匹配,取 updated 最近的一个);无则新建。
// opencode 会话持久化在磁盘(/root/.local/share/opencode),进程或后端重启后仍可据此
// 恢复开发者上次的编码上下文,而非每次打开都新建空会话。
func ensureSession(port int, repoDir string) string {
	resp, err := wsHTTPClient.Get(fmt.Sprintf("http://127.0.0.1:%d/api/session", port))
	if err == nil {
		var r struct {
			Data []struct {
				ID       string `json:"id"`
				Time     struct{ Updated int64 `json:"updated"` } `json:"time"`
				Location struct{ Directory string `json:"directory"` } `json:"location"`
			} `json:"data"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&r)
		resp.Body.Close()
		var bestID string
		var bestT int64
		match := 0
		for _, s := range r.Data {
			if s.Location.Directory == repoDir {
				match++
				if s.Time.Updated > bestT {
					bestID = s.ID
					bestT = s.Time.Updated
				}
			}
		}
		if bestID != "" {
			log.Printf("[codews] 复用 opencode 会话 %s (repo=%s, 匹配 %d/%d)", bestID, repoDir, match, len(r.Data))
			return bestID // 复用最近会话
		}
		log.Printf("[codews] 新建 opencode 会话 (repo=%s, 现有 %d 个均不匹配 directory)", repoDir, len(r.Data))
	} else {
		log.Printf("[codews] 查询 opencode 会话失败将新建: %v", err)
	}
	return initSession(port) // 无匹配 → 新建
}

// initSession 在新启动的工作台上预创建一个会话(POST http://127.0.0.1:port/session)。
// serve 刚 listen 时 API 可能短暂未就绪, 故重试几次; 持续失败返回空串(非致命)。
func initSession(port int) string {
	url := fmt.Sprintf("http://127.0.0.1:%d/session", port)
	for i := 0; i < 4; i++ {
		resp, err := wsHTTPClient.Post(url, "application/json", bytes.NewBufferString("{}"))
		if err != nil {
			time.Sleep(300 * time.Millisecond)
			continue
		}
		var r struct {
			ID string `json:"id"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&r)
		resp.Body.Close()
		if r.ID != "" {
			return r.ID
		}
		time.Sleep(300 * time.Millisecond)
	}
	return ""
}

// Get 取某开发者在该应用的活跃会话；否则 nil。
func (m *Manager) Get(appID, userID string) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[appID+":"+userID]; ok && s.alive() {
		return s
	}
	return nil
}

func (m *Manager) allocPortLocked() int {
	used := map[int]bool{}
	for _, s := range m.sessions {
		used[s.Port] = true
	}
	for p := portMin; p <= portMax; p++ {
		if !used[p] {
			return p
		}
	}
	return 0
}

// waitListen 轮询 TCP 连通直到超时。
func waitListen(port int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	for time.Now().Before(deadline) {
		if conn, err := net.DialTimeout("tcp", addr, 300*time.Millisecond); err == nil {
			_ = conn.Close()
			return true
		}
		time.Sleep(300 * time.Millisecond)
	}
	return false
}

// alive 进程是否仍在运行（Wait 未返回）。
func (s *Session) alive() bool {
	return s != nil && s.cmd != nil && s.cmd.ProcessState == nil
}
