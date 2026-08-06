// Package codews 管理 AI 编码工具的交互式工作台（opencode / Claude Code / Codex ...）。
// 不造轮子：复用各工具自带的 web/headless 服务，开发者浏览器访问即得原生编码体验。
//
// 工作模型：为每个应用启动一个工具实例（cwd=应用 repo），监听 0.0.0.0:<port>；
// compose 把 9400-9450 映射到宿主；开发者访问 http://<host>:<port> 即该工具的官方界面，
// 编码产出 commit 到 repo，无缝衔接 ANP 的版本/发布流程。
package codews

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"zhiyuan-anp/platform/backend/internal/config"
)

const (
	portMin     = 9400
	portMax     = 9450
	defaultTool = "opencode"
	// maxTurnsPerReq 同一需求会话内 user 提问轮次上限：超过则轮转到新会话，
	// 防止单需求历史无限累积烧 token（与 opencode autocompact 互补）。可按需调。
	maxTurnsPerReq = 20
)

// Manager 管理各应用的编码工作台进程（可插拔多工具）。
type Manager struct {
	host       string
	cfg        *config.Store // 系统配置（取智谱key/claude端点注入子进程）；nil=不注入
	sessionLog SessionStore  // 会话持久化（绩效/互动统计）；nil=纯内存兼容
	mu         sync.Mutex
	sessions   map[string]*Session // appID -> 当前活跃工作台
	ports      map[int]bool        // 端口注册表：allocPort 即刻登记，进程 cmd.Wait 后释放（独立于 sessions，防并发/kill 后重用同端口）
	tools      map[string]Tool
}

// Session 一个开发者在一个应用上的编码工作台实例（per user × app × tool）。
type Session struct {
	AppID   string `json:"app_id"`
	UserID  string `json:"user_id"` // 开发者（不同开发者可各开各的工作台/工具）
	Tool    string `json:"tool"`
	Port    int    `json:"port"`
	URL     string `json:"url"`
	RepoDir string `json:"-"`
	// SessionID 预创建的会话(带项目上下文); 开发者打开 web UI 即见此会话而非空白。
	// 空=未预创建或失败(非致命, 用户可手动新建)。
	SessionID string `json:"session_id,omitempty"`
	// DeepURL 直达预创建会话的深链接(/<base64url(repoDir)>/session/<id>);
	// 前端优先打开它, 省去用户在根路径手点会话。空=未预创建, 回退用 URL。
	DeepURL string `json:"deep_url,omitempty"`
	// RequirementID 绑定的需求（工作直播按此关联；空=application 页老入口）。
	RequirementID string `json:"-"`
	cmd           *exec.Cmd
	started       time.Time
	logID         string // 落库的 codews_session.id（sessionLog 持久化用）
}

// NewManager 构造，预注册 opencode（已接入）+ claude/codex（接口预留）。
func NewManager(host string, cfg *config.Store) *Manager {
	m := &Manager{host: host, cfg: cfg, sessions: map[string]*Session{}, ports: map[int]bool{}, tools: map[string]Tool{}}
	m.Register(OpenCodeTool{})
	m.Register(ClaudeTool{})
	m.Register(CodexTool{})
	return m
}

// SetSessionLogger 注入会话持久化（绩效/互动统计）。nil=纯内存兼容（测试/未启用）。
// 单独 setter 避免 NewManager 签名变更波及大量既有调用方与测试。
func (m *Manager) SetSessionLogger(l SessionStore) { m.sessionLog = l }

// toolEnv 按工具构造子进程环境变量：claude 注入智谱 anthropic 兼容端点
// （ANTHROPIC_BASE_URL/AUTH_TOKEN/MODEL），key 复用 zhipuai_api_key；其他工具返回 nil（继承 os.Environ）。
func (m *Manager) toolEnv(toolName string) []string {
	if m.cfg == nil || toolName != "claude" {
		return nil
	}
	key := m.cfg.Get("zhipuai_api_key", "")
	if key == "" {
		return nil
	}
	return []string{
		"ANTHROPIC_BASE_URL=" + m.cfg.Get("claude_base_url", "https://open.bigmodel.cn/api/anthropic"),
		"ANTHROPIC_AUTH_TOKEN=" + key,
		"ANTHROPIC_MODEL=" + m.cfg.Get("claude_model", "glm-4.6"),
	}
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
func (m *Manager) Ensure(psID, appID, repoDir, userID, toolName, reqID string) (*Session, error) {
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
	old := m.sessions[key] // 旧会话（可能 nil）；用于判定换需求是否强制新建
	forceNew := shouldForceNewForRequirement(old, reqID)
	// 同开发者同工具 且 需求未变 → 复用（reqID 空=沿用现有，刷新不破坏已绑定会话）
	if s, exists := m.sessions[key]; exists && s.alive() && s.Tool == toolName && (reqID == "" || s.RequirementID == reqID) {
		m.mu.Unlock()
		return s, nil
	}
	// 换工具 或 显式换需求（reqID 非空且不同）→ 停旧起新（换需求=新会话，杜绝多需求串台）
	if old, exists := m.sessions[key]; exists && old.cmd != nil && old.cmd.Process != nil && (old.Tool != toolName || (reqID != "" && old.RequirementID != reqID)) {
		_ = old.cmd.Process.Kill()
		delete(m.sessions, key)
	}
	port := m.allocPortLocked()
	if port == 0 {
		m.mu.Unlock()
		return nil, fmt.Errorf("无可用工作台端口(%d-%d)", portMin, portMax)
	}
	m.mu.Unlock()

	// 开发者隔离:在独立 worktree(分支 dev-<user>)编码,多人不互改
	workDir := ensureWorktree(repoDir, userID)
	cmd, err := tool.Start(workDir, port, m.toolEnv(toolName))
	if err != nil {
		m.mu.Lock()
		delete(m.ports, port) // Start 失败：归还预留端口，避免端口表泄漏
		m.mu.Unlock()
		return nil, err
	}
	s := &Session{
		AppID: appID, UserID: userID, Tool: toolName, Port: port, RepoDir: workDir, cmd: cmd, started: time.Now(),
		RequirementID: reqID,
		URL:           fmt.Sprintf("http://%s:%d", m.host, port),
	}
	m.mu.Lock()
	m.sessions[key] = s
	m.mu.Unlock()

	// 进程退出时清理（端口回收 + 会话计数回填）
	go func() {
		_ = cmd.Wait()
		if m.sessionLog != nil && s.logID != "" {
			_ = m.sessionLog.FinishSession(context.Background(), s.logID, m.finishCounts(s))
		}
		m.mu.Lock()
		delete(m.ports, s.Port) // 进程已退出，无条件归还端口（与是否当前 session 无关）
		if cur, ok := m.sessions[key]; ok && cur == s {
			delete(m.sessions, key) // 仅当仍是当前 session 时才从 map 移除（并发起新的不误删）
		}
		m.mu.Unlock()
	}()

	if !waitListen(port, 6*time.Second) {
		return nil, fmt.Errorf("%s 工作台启动后未监听 :%d", toolName, port)
	}
	// opencode 专属：复用/预创建会话（claude/codex 走 ttyd，无对等 session API，跳过——靠工具自身 --continue 恢复）
	if toolName == "opencode" {
		// 复用 opencode 已有会话(按 repo 目录匹配,取最近);无则预创建一个带项目上下文的会话。
		// opencode 会话持久化在磁盘,进程/后端重启后据此恢复开发者上次的编码上下文,不再每次新建。失败非致命。
		// opencode 上报的 location.directory 是它自己的 cwd（worktree），
		// 会话匹配 / 深链接 slug 都须用 workDir，否则永 mismtach → 每次新建会话、深链接打不开。
		s.SessionID = ensureSession(port, workDir, forceNew)
		if s.SessionID != "" {
			s.DeepURL = sessionDeepURL(s.URL, workDir, s.SessionID)
		}
	}
	// 落库 codews_session（绩效/互动统计；失败仅 log，不阻塞工作台）
	// RepoDir 记 worktree 路径（工具实际 cwd = transcript 的 cwd），ReaderFor 据此匹配 transcript。
	if m.sessionLog != nil {
		rec := &SessionRecord{ProjectSpaceID: psID, AppID: appID, UserID: userID, Tool: toolName, RepoDir: workDir, Port: port, SessionID: s.SessionID, RequirementID: reqID}
		if err := m.sessionLog.StartSession(context.Background(), rec); err == nil {
			s.logID = rec.ID
		} else {
			log.Printf("[codews] 落库 codews_session 失败(非致命): %v", err)
		}
	}
	return s, nil
}

// finishCounts 会话结束时统计交互计数：用 TranscriptReader 读工具原生 transcript，
// 数 user 消息=prompt 数、总消息数=message 数。读失败/无 reader → 零值（仅 ended_at 回填）。
func (m *Manager) finishCounts(s *Session) SessionCounts {
	r := ReaderFor(s.Tool)
	if r == nil {
		return SessionCounts{} // opencode 走 live HTTP，磁盘 reader 无（计数 best-effort 留 0）
	}
	msgs, err := r.Messages(s.RepoDir, s.SessionID)
	if err != nil || len(msgs) == 0 {
		return SessionCounts{}
	}
	var prompts int
	for _, mm := range msgs {
		if mm.Role == "user" {
			prompts++
		}
	}
	return SessionCounts{PromptCount: prompts, MessageCount: len(msgs)}
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

// sessionListClient 列 opencode 会话用:/api/session 响应含全部会话 + token 统计。
// 超时需覆盖两种情况：① serve 刚 listen 但 HTTP handler 未就绪（请求挂起）——由 ensureSession
// 的重试循环兜底；② 会话累积较多时序列化偏慢——需足够长的单次超时，否则误判超时→initSession
// 不断新建→会话越多越慢的死亡螺旋（F-3）。故给 10s（重试主要服务情形①的连接级失败）。
var sessionListClient = &http.Client{Timeout: 10 * time.Second}

// shouldForceNewForRequirement 判定是否因切换需求需强制新建 opencode 会话。
// 换需求(旧会话绑了不同 RequirementID，或旧会话无绑定而现在选了需求)→ true，杜绝跨需求历史串台/累积。
// 首次(无旧会话) / 没选需求(reqID 空) / 同需求 → false。
func shouldForceNewForRequirement(old *Session, reqID string) bool {
	return old != nil && reqID != "" && old.RequirementID != reqID
}

// ensureSession 复用 opencode 已有会话(按 repo 目录匹配,取 updated 最近的一个);无则新建。
// forceNew=true(换需求)时跳过复用直接新建——避免按 workDir 把上一个需求的会话捞回,真正按需求隔离。
// opencode 会话持久化在磁盘(/root/.local/share/opencode),进程或后端重启后仍可据此
// 恢复开发者上次的编码上下文,而非每次打开都新建空会话。
func ensureSession(port int, repoDir string, forceNew bool) string {
	if forceNew {
		log.Printf("[codews] forceNew 新建 opencode 会话 (repo=%s)", repoDir)
		return initSession(port)
	}
	// opencode serve 刚 listen 时 HTTP handler 可能尚未就绪（请求挂起直至起来），
	// 用短超时 + 重试等就绪；就绪后 /api/session 仅几毫秒。
	for i := 0; i < 6; i++ {
		resp, err := sessionListClient.Get(fmt.Sprintf("http://127.0.0.1:%d/api/session", port))
		if err != nil {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		var r struct {
			Data []struct {
				ID   string `json:"id"`
				Time struct {
					Updated int64 `json:"updated"`
				} `json:"time"`
				Location struct {
					Directory string `json:"directory"`
				} `json:"location"`
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
		return initSession(port) // 查到了但无匹配 → 新建
	}
	log.Printf("[codews] 查询 opencode 会话重试6次仍失败将新建")
	return initSession(port)
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

// SessionMessages 返回某开发者当前 opencode 会话的对话文本（user/assistant 消息）。
// 供登记变更时自动总结对话内容，免手填。无活跃会话/无消息返回空串（非致命）。
func (m *Manager) SessionMessages(appID, userID string) (string, error) {
	s := m.Get(appID, userID)
	if s == nil || s.SessionID == "" {
		return "", nil
	}
	msgs, err := LiveTranscript(s.Port, s.SessionID)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	for _, mm := range msgs {
		sb.WriteString("[" + mm.Role + "] " + mm.Content + "\n")
	}
	return strings.TrimSpace(sb.String()), nil
}

// countUserTurns 数 opencode 会话中 role=user 的消息条数（≈用户提问轮次）。
// 读失败/无会话返回 0（非致命，不阻断发 prompt）。
func countUserTurns(port int, sessionID string) int {
	msgs, err := LiveTranscript(port, sessionID)
	if err != nil || len(msgs) == 0 {
		return 0
	}
	n := 0
	for _, m := range msgs {
		if m.Role == "user" {
			n++
		}
	}
	return n
}

// SendPrompt 向某开发者当前 opencode 会话发送一条 prompt(注入需求/指令),
// opencode AI 在工作台实时响应(流式),开发者可看编码过程并随时介入。
// 同需求会话累计 user 轮次达 maxTurnsPerReq 时，先轮转到新会话再发，避免历史滚雪球。
func (m *Manager) SendPrompt(appID, userID, text string) error {
	s := m.Get(appID, userID)
	if s == nil || s.SessionID == "" {
		return fmt.Errorf("无活跃编码会话(请先打开工作台)")
	}
	// 轮次双限：同需求会话达上限 → 新建会话轮转（DeepURL 一并由 handler 回传前端跳转）
	if countUserTurns(s.Port, s.SessionID) >= maxTurnsPerReq {
		if newID := initSession(s.Port); newID != "" {
			s.SessionID = newID
			s.DeepURL = sessionDeepURL(s.URL, s.RepoDir, newID)
			log.Printf("[codews] 需求会话达 %d 轮, 轮转到新会话 %s", maxTurnsPerReq, newID)
		}
	}
	body, _ := json.Marshal(map[string]interface{}{
		"prompt": map[string]string{"text": text},
	})
	resp, err := wsHTTPClient.Post(fmt.Sprintf("http://127.0.0.1:%d/api/session/%s/prompt", s.Port, s.SessionID), "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

// ensureWorktree 为开发者创建/复用独立 git worktree(分支 dev-<user>),opencode 在此隔离编码,多人不互改。
// 健壮处理：merge 后 worktree 目录被删但 git 仍注册为 prunable、且 dev-<user> 分支保留 →
// 直接 `worktree add -b` 会失败。先 prune 清残留，分支已存在时 checkout 已有分支，仍失败则回退主仓。
func ensureWorktree(repoDir, userID string) string {
	wt := filepath.Join(repoDir, ".worktrees", sanitizeID(userID))
	if _, err := os.Stat(filepath.Join(wt, ".git")); err == nil {
		return wt // worktree 已存在且有效
	}
	// wt 目录存在但无 .git（merge 后残留空目录）→ 删掉，让 worktree add 干净建
	if _, err := os.Stat(wt); err == nil {
		_ = os.RemoveAll(wt)
	}
	_ = os.MkdirAll(filepath.Dir(wt), 0755)
	branch := "dev-" + sanitizeID(userID)
	// 清 prunable 残留（git 仍注册已删的 worktree，会导致 add 报 already exists）
	_ = exec.Command("git", "-C", repoDir, "worktree", "prune").Run()
	// 先 add -b 建新分支；分支已存在（merge 保留了分支）则 checkout 已有分支
	if err := exec.Command("git", "-C", repoDir, "worktree", "add", "-b", branch, wt).Run(); err != nil {
		_ = exec.Command("git", "-C", repoDir, "worktree", "add", wt, branch).Run()
	}
	if _, err := os.Stat(filepath.Join(wt, ".git")); err != nil {
		// 兜底：两步都没建成（主仓异常等）→ 回退主仓，避免 opencode chdir 到无效目录整个起不来
		log.Printf("[codews] ensureWorktree 建立失败，回退主仓: %s", repoDir)
		return repoDir
	}
	_ = exec.Command("git", "-C", wt, "config", "user.email", "anp@platform").Run()
	_ = exec.Command("git", "-C", wt, "config", "user.name", "ANP "+userID).Run()
	return wt
}

// sanitizeID 把 userID 转为 git 友好的分支/目录名(小写字母数字,-分隔)。
func sanitizeID(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	if b.Len() == 0 {
		return "dev"
	}
	return b.String()
}

// allocPortLocked 分配一个空闲工作台端口。端口注册表 m.ports 独立于 sessions：
// 端口在 allocPort 时即刻登记，直到对应进程 cmd.Wait 返回（由 session 的清理 goroutine
// 负责）才释放。这样 ① 换需求 kill 旧进程后端口不会立刻被新一轮 allocPort 重用——旧进程
// 未死、端口未释放，直接重用会 EADDRINUSE 起不来；② 并发 Ensure（rapid 切需求触发）也
// 抢不到同一个尚未登记完的端口。必须由持有 m.mu 的调用方调用。
func (m *Manager) allocPortLocked() int {
	for p := portMin; p <= portMax; p++ {
		if !m.ports[p] {
			m.ports[p] = true
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
