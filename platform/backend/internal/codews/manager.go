// Package codews 管理 AI 编码工具的交互式工作台（opencode / Claude Code / Codex ...）。
// 不造轮子：复用各工具自带的 web/headless 服务，开发者浏览器访问即得原生编码体验。
//
// 工作模型：为每个应用启动一个工具实例（cwd=应用 repo），监听 0.0.0.0:<port>；
// compose 把 9400-9499 映射到宿主；开发者访问 http://<host>:<port> 即该工具的官方界面，
// 编码产出 commit 到 repo，无缝衔接 ANP 的版本/发布流程。
// 端口池 9400-9499（100 个）是全局容量上限；Start() 启后台 reaper 驱逐空闲会话以回收端口。
package codews

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
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
	portMax     = 9499 // 端口池上限（含）；100 个并发工作台（原 9450=51 个）。须与 compose 端口映射保持一致。
	defaultTool = "opencode"

	// codewsXDGBase per-user opencode config 的根目录（XDG_CONFIG_HOME 指向其子目录）。
	// 仅挪 config（opencode.json），不碰共享 1GB 的 opencode.db（HOME 整体覆盖会每用户复制，出局）。
	// 可用 CODEWS_XDG_BASE env 覆盖（测试/本地调试，见 xdgConfigBase）。
	codewsXDGBase = "/root/.cache/anp-codews"
)

// ModelConfigWriter 解耦 codews ↔ compute：codews 不直接 import compute 包，
// 仅依赖这两个方法（*compute.Store 在 Task 1 已实现）。用于 per-user opencode config
// 生成 + claude ANTHROPIC_MODEL 名解析。nil=兜底全局 config（未授权任何模型的用户）。
type ModelConfigWriter interface {
	WriteOpenCodeConfigForModels(ctx context.Context, modelIDs []string, path string) error
	ModelName(ctx context.Context, modelID string) (string, error)
	// ResolveOpencodeModelID 把 compute_model.id（cmd_xxx）解析为 opencode 的 "providerID/modelID"
	// （provider 段=slugify(providerName)，与 opencode.json 的 provider key 一致）。
	// 建会话时据此指定 model，否则 opencode 退回内置默认（免费）模型。未命中返 ("", nil)。
	ResolveOpencodeModelID(ctx context.Context, modelID string) (string, error)
}

// Manager 管理各应用的编码工作台进程（可插拔多工具）。
type Manager struct {
	host       string
	cfg        *config.Store     // 系统配置（取智谱key/claude端点注入子进程）；nil=不注入
	sessionLog SessionStore      // 会话持久化（绩效/互动统计）；nil=纯内存兼容
	writer     ModelConfigWriter // per-user 模型授权（写 opencode config + 解析模型名）；nil=全局兜底
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
	// Model 当前会话注入的授权模型 id（cmd_xxx）；空=走全局 config（未授权/兜底）。
	// 内存态，不持久化、不进 SessionRecord（每次 Ensure 按入参重生成 per-user config）。
	Model    string `json:"-"`
	cmd      *exec.Cmd
	started  time.Time
	lastUsed time.Time // 最近一次活动（Ensure/Get 刷新）；reaper 据此驱逐空闲会话回收端口
	logID    string    // 落库的 codews_session.id（sessionLog 持久化用）
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

// SetModelAccess 注入 per-user 模型授权（写 per-user opencode config + 解析模型名）。
// nil=兜底全局 config（未授权任何模型的用户，渐进迁移）。单独 setter 避免 NewManager
// 签名变更波及既有调用方（Task 3 在 main.go/appdeploy.Register 构造后调用）。
func (m *Manager) SetModelAccess(w ModelConfigWriter) { m.writer = w }

// buildEnv 按工具构造子进程环境变量（替代旧 toolEnv，加入 per-user 模型注入）。
//
// opencode：model 非空且 writer 已注入 → 写 per-user opencode config（仅授权模型）到 XDG
//
//	目录，返回 XDG_CONFIG_HOME=<dir>（opencode v1.18.9 认此变量读 config）。
//	model 空 / writer 未注入 / 写失败 → 返 nil（继承 os.Environ，用全局默认 config，兜底）。
//
// claude：注入智谱 anthropic 兼容端点（ANTHROPIC_BASE_URL/AUTH_TOKEN/MODEL）；
//
//	ANTHROPIC_MODEL 取授权模型名（model→writer.ModelName），空/解析失败 → 沿用全局
//	claude_model。key(zhipuai_api_key) 空 → 不注入（与旧行为一致）。
//
// 其他工具：返回 nil（继承 os.Environ）。
func (m *Manager) buildEnv(ctx context.Context, toolName, model, appID, userID string) []string {
	switch toolName {
	case "opencode":
		if model == "" || m.writer == nil {
			return nil // 无授权模型 → 用全局 config（兜底，渐进迁移）
		}
		dir := xdgConfigDir(xdgConfigBase(), appID, userID)
		cfgPath := filepath.Join(dir, "opencode", "opencode.json")
		if err := m.writer.WriteOpenCodeConfigForModels(ctx, []string{model}, cfgPath); err != nil {
			// 降级：不阻断会话，回退全局 config（功能可用但模型隔离失效）
			log.Printf("[codews] 写 per-user opencode config 失败(降级用全局): app=%s user=%s model=%s err=%v", appID, userID, model, err)
			return nil
		}
		return buildOpenCodeEnv(dir)
	case "claude":
		if m.cfg == nil {
			return nil
		}
		key := m.cfg.Get("zhipuai_api_key", "")
		if key == "" {
			return nil
		}
		name := m.cfg.Get("claude_model", "glm-4.6")
		if model != "" && m.writer != nil {
			if n, err := m.writer.ModelName(ctx, model); err == nil && n != "" {
				name = n
			} else if err != nil {
				log.Printf("[codews] 解析授权模型名失败(沿用全局 claude_model): model=%s err=%v", model, err)
			}
		}
		return buildClaudeEnv(m.cfg.Get("claude_base_url", "https://open.bigmodel.cn/api/anthropic"), key, name)
	default:
		return nil
	}
}

// xdgConfigBase per-user opencode config 的根目录。CODEWS_XDG_BASE env 可覆盖（测试/本地），
// 默认 codewsXDGBase（容器内可写；不碰共享 opencode.db）。
func xdgConfigBase() string {
	if b := os.Getenv("CODEWS_XDG_BASE"); b != "" {
		return b
	}
	return codewsXDGBase
}

// xdgConfigDir 推导某 (app,user) 的 per-user XDG 目录：base/<sanitize(appID)>-<sanitize(userID)>。
// sanitize 复用 sanitizeID（git/文件系统友好），确保跨用户/应用目录稳定可复算。
func xdgConfigDir(base, appID, userID string) string {
	return filepath.Join(base, sanitizeID(appID)+"-"+sanitizeID(userID))
}

// buildOpenCodeEnv opencode 子进程注入 XDG_CONFIG_HOME=<dir>（opencode v1.18.9 认此变量读 config）。
// dir 空 → 返 nil（继承 os.Environ，用全局默认 config）。
func buildOpenCodeEnv(xdgDir string) []string {
	if xdgDir == "" {
		return nil
	}
	return []string{"XDG_CONFIG_HOME=" + xdgDir}
}

// buildClaudeEnv claude 子进程注入智谱 anthropic 兼容端点（3 个 ANTHROPIC_* 变量）。
func buildClaudeEnv(baseURL, apiKey, modelName string) []string {
	return []string{
		"ANTHROPIC_BASE_URL=" + baseURL,
		"ANTHROPIC_AUTH_TOKEN=" + apiKey,
		"ANTHROPIC_MODEL=" + modelName,
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
// model 空=兜底全局 config（未授权任何模型）；非空 → 写 per-user XDG config（仅授权模型）注入子进程。
// reqForceNew=true 强制开空会话（前端「🆕 新会话」按钮）；冷启动后端重启时按库最近会话跨需求自动新建。
// 同一开发者切换工具会停旧起新；不同开发者各自独立工作台（可不同工具）。
func (m *Manager) Ensure(psID, appID, repoDir, userID, toolName, reqID, model string, reqForceNew bool) (*Session, error) {
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
	old := m.sessions[key]                                    // 旧会话（可能 nil）；用于判定换需求是否强制新建
	forceNew := computeForceNew(old, reqID, reqForceNew, nil) // 先按内存+请求级；冷启动查库后重算（见解锁后）
	// 同开发者同工具 且 需求未变 且 非强制新建 → 复用（reqID 空=沿用现有，刷新不破坏已绑定会话）
	if s, exists := m.sessions[key]; exists && s.alive() && s.Tool == toolName && !forceNew && (reqID == "" || s.RequirementID == reqID) {
		s.lastUsed = time.Now() // 复用即视为活动，刷新以免被 reaper 误驱逐
		m.mu.Unlock()
		return s, nil
	}
	// 换工具 或 强制新建（换需求/请求级）→ 停旧起新（换需求=新会话，杜绝多需求串台）
	if old, exists := m.sessions[key]; exists && old.cmd != nil && old.cmd.Process != nil && (old.Tool != toolName || forceNew) {
		_ = old.cmd.Process.Kill()
		delete(m.sessions, key)
	}
	port := m.allocPortLocked()
	if port == 0 {
		m.mu.Unlock()
		return nil, fmt.Errorf("无可用工作台端口(%d-%d)", portMin, portMax)
	}
	m.mu.Unlock()

	// 冷启动(后端重启→内存 old==nil)：查库最近会话绑的需求，若与本次不同则强制新建，
	// 避免 ensureSession 按 workDir 把上个需求的臃肿会话捞回（跨需求串台 + token 浪费）。
	if !forceNew && old == nil && reqID != "" && m.sessionLog != nil {
		if last, err := m.sessionLog.LastSession(context.Background(), psID, appID, userID); err != nil {
			log.Printf("[codews] 查最近会话失败(非致命): %v", err)
		} else {
			forceNew = computeForceNew(old, reqID, reqForceNew, last)
			if forceNew {
				log.Printf("[codews] 冷启动跨需求(库=%s 现=%s)→forceNew 新建会话", last.RequirementID, reqID)
			}
		}
	}

	// 开发者隔离:在独立 worktree(分支 dev-<user>)编码,多人不互改
	workDir, err := ensureWorktree(repoDir, userID)
	if err != nil {
		m.mu.Lock()
		delete(m.ports, port) // worktree 建不起来：归还预留端口（与 Start 失败同处理）
		m.mu.Unlock()
		return nil, err
	}
	// 模型注入：opencode → 写 per-user XDG config（仅授权模型）并注入 XDG_CONFIG_HOME；
	// claude → ANTHROPIC_MODEL 取授权模型名。model 空 / writer 未注入 → 兜底全局 config（不阻断）。
	env := m.buildEnv(context.Background(), toolName, model, appID, userID)
	cmd, err := tool.Start(workDir, port, env)
	if err != nil {
		m.mu.Lock()
		delete(m.ports, port) // Start 失败：归还预留端口，避免端口表泄漏
		m.mu.Unlock()
		return nil, err
	}
	s := &Session{
		AppID: appID, UserID: userID, Tool: toolName, Port: port, RepoDir: workDir, cmd: cmd, started: time.Now(), lastUsed: time.Now(),
		RequirementID: reqID,
		Model:         model,
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
		// 解析授权模型为 opencode Model.Ref（"providerID/modelID"），建会话时带上，确保用真模型而非
		// opencode 内置免费默认模型。空/解析失败 → modelRef=""，initSession 退回 body={}（向后兼容）。
		var modelRef string
		if model != "" && m.writer != nil {
			if ref, err := m.writer.ResolveOpencodeModelID(context.Background(), model); err == nil && ref != "" {
				modelRef = ref
			} else if err != nil {
				log.Printf("[codews] 解析 opencode 模型 ref 失败(会话沿用默认模型): model=%s err=%v", model, err)
			}
		}
		// 复用 opencode 已有会话(按 repo 目录匹配,取最近);无则预创建一个带项目上下文的会话。
		// opencode 会话持久化在磁盘,进程/后端重启后据此恢复开发者上次的编码上下文,不再每次新建。失败非致命。
		// opencode 上报的 location.directory 是它自己的 cwd（worktree），
		// 会话匹配 / 深链接 slug 都须用 workDir，否则永 mismtach → 每次新建会话、深链接打不开。
		s.SessionID = ensureSession(port, workDir, forceNew, modelRef)
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

// computeForceNew 综合判定是否强制新建 opencode 会话（跳过磁盘复用直接 initSession）：
//  1. 请求级显式新建 reqForceNew（前端「🆕 新会话」按钮）
//  2. 内存旧会话绑了不同需求（shouldForceNewForRequirement）
//  3. 冷启动 old==nil（后端重启后内存空）：库里最近会话(last)绑了不同需求
//
// last 仅冷启动时由调用方查库传入；非冷启动或未启用持久化时传 nil。
func computeForceNew(old *Session, reqID string, reqForceNew bool, last *SessionRecord) bool {
	if reqForceNew {
		return true
	}
	if shouldForceNewForRequirement(old, reqID) {
		return true
	}
	if old == nil && reqID != "" && last != nil && last.RequirementID != "" && last.RequirementID != reqID {
		return true
	}
	return false
}

// ensureSession 复用 opencode 已有会话(按 repo 目录匹配,取 updated 最近的一个);无则新建。
// forceNew=true(换需求)时跳过复用直接新建——避免按 workDir 把上一个需求的会话捞回,真正按需求隔离。
// opencode 会话持久化在磁盘(/root/.local/share/opencode),进程或后端重启后仍可据此
// 恢复开发者上次的编码上下文,而非每次打开都新建空会话。
func ensureSession(port int, repoDir string, forceNew bool, modelRef string) string {
	if forceNew {
		log.Printf("[codews] forceNew 新建 opencode 会话 (repo=%s)", repoDir)
		return initSession(port, modelRef)
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
		return initSession(port, modelRef) // 查到了但无匹配 → 新建
	}
	log.Printf("[codews] 查询 opencode 会话重试6次仍失败将新建")
	return initSession(port, modelRef)
}

// initSession 在新启动的工作台上预创建一个会话(POST http://127.0.0.1:port/session)。
// serve 刚 listen 时 API 可能短暂未就绪, 故重试几次; 持续失败返回空串(非致命)。
//
// modelRef 形如 "providerID/modelID"（ResolveOpencodeModelID 产出）：非空时建会话带
// {"model":{"providerID","id"}}，让 opencode 用授权模型；空则 body={}，opencode 退回内置
// 默认（免费）模型——向后兼容未授权/解析失败场景。
func initSession(port int, modelRef string) string {
	url := fmt.Sprintf("http://127.0.0.1:%d/session", port)
	body := "{}"
	if modelRef != "" {
		// opencode 建会话的 model 需 Model.Ref 对象 {"providerID","id"}（非字符串 "p/m"）。
		parts := strings.SplitN(modelRef, "/", 2)
		if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
			b, _ := json.Marshal(map[string]any{
				"model": map[string]string{"providerID": parts[0], "id": parts[1]},
			})
			body = string(b)
		}
	}
	for i := 0; i < 4; i++ {
		resp, err := wsHTTPClient.Post(url, "application/json", bytes.NewBufferString(body))
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
		s.lastUsed = time.Now() // 读取即活动（SendPrompt/SessionMessages/handler 都经此），刷新免被驱逐
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

// SendPrompt 向某开发者当前 opencode 会话发送一条 prompt(注入需求/指令),
// opencode AI 在工作台实时响应(流式),开发者可看编码过程并随时介入。
// 不自动轮转/截断——为保能力，token 控制交给"按需求隔离"（换需求自动新开会话）
// + 任务完成时前端提醒用户认领下一需求新开会话，避免硬切清零上下文。
//
// 端点必须用 opencode web SPA 同款的 POST /session/<id>/prompt_async + parts 结构。
// 旧实现用 /api/session/<id>/prompt {prompt:{text}}——该端点虽返回 admitted，但实测
// 不落任何消息（headless 探针 probe9：ANP 精确流程建会+发送后全程 0 条消息），会话
// 深链接打开即一片空白。改用 prompt_async 后消息真正写入、AI 流式回复、深链渲染可见
// （probe8/9/10 验证：最小 body {messageID, parts:[{type:"text",text}]} 即生效，模型
// 由会话建会时继承，发送无需重传 agent/model）。
func (m *Manager) SendPrompt(appID, userID, text string) error {
	s := m.Get(appID, userID)
	if s == nil || s.SessionID == "" {
		return fmt.Errorf("无活跃编码会话(请先打开工作台)")
	}
	body, _ := json.Marshal(map[string]interface{}{
		"messageID": opencodeClientID("msg_"),
		"parts": []map[string]interface{}{
			{"id": opencodeClientID("prt_"), "type": "text", "text": text},
		},
	})
	resp, err := wsHTTPClient.Post(fmt.Sprintf("http://127.0.0.1:%d/session/%s/prompt_async", s.Port, s.SessionID), "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

// opencodeClientID 生成 opencode web SPA 同款 client 侧 ID（prefix + 16 hex，如 msg_/prt_）。
// prompt_async 的 messageID / parts[].id 由 client 自生成；仅需唯一，格式无强约束（探针实测）。
func opencodeClientID(prefix string) string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand: " + err.Error())
	}
	return prefix + hex.EncodeToString(b)
}

// ensureWorktree 为开发者创建/复用独立 git worktree(分支 dev-<user>),opencode 在此隔离编码,多人不互改。
// 健壮处理：merge 后 worktree 目录被删但 git 仍注册为 prunable、且 dev-<user> 分支保留 →
// 直接 `worktree add -b` 会失败。先 prune 清残留，分支已存在时 checkout 已有分支。
// 两步都建不成（主仓非 git 仓库/异常）→ 返回 error：不再静默回退主仓（旧实现 return repoDir 会让
// opencode 把改动直接 commit 进主线、污染他人分支），改由 Ensure 向上抛，启动失败可见可修。
func ensureWorktree(repoDir, userID string) (string, error) {
	wt := filepath.Join(repoDir, ".worktrees", sanitizeID(userID))
	if _, err := os.Stat(filepath.Join(wt, ".git")); err == nil {
		return wt, nil // worktree 已存在且有效
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
		log.Printf("[codews] ensureWorktree 建立失败(不回退主仓): repoDir=%s user=%s", repoDir, userID)
		return "", fmt.Errorf("建立开发者 worktree 失败(user=%s)：请确认应用仓库 %s 已初始化为有效 git 仓库", userID, repoDir)
	}
	_ = exec.Command("git", "-C", wt, "config", "user.email", "anp@platform").Run()
	_ = exec.Command("git", "-C", wt, "config", "user.name", "ANP "+userID).Run()
	return wt, nil
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

// idleTimeout 返回空闲会话驱逐阈值（CODEWS_IDLE_TIMEOUT env 可调，如 "2h"/"90m"；默认 2h，≤0 禁用）。
// 开发者在 opencode 内纯编码（不经 ANP HTTP 路径）时不刷新 lastUsed，故默认值不宜过短——2h 兼顾
// 回收隔夜/长闲置会话与不误杀长时间专注编码。被驱逐后会话/分支/worktree 持久在磁盘，开发者再开
// 工作台即自动重连（非破坏性），仅回收 opencode 进程与端口。
func idleTimeout() time.Duration {
	d := 2 * time.Hour
	if v := os.Getenv("CODEWS_IDLE_TIMEOUT"); v != "" {
		if parsed, err := time.ParseDuration(v); err == nil {
			d = parsed
		}
	}
	return d
}

// idleVictims 返回超阈值未活动的活跃会话（纯决策，不 kill，便于单测）。
// 由后台 reaper 调用，不在请求路径调用。
func (m *Manager) idleVictims(now time.Time) []*Session {
	timeout := idleTimeout()
	if timeout <= 0 {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var victims []*Session
	for _, s := range m.sessions {
		if s.alive() && now.Sub(s.lastUsed) > timeout {
			victims = append(victims, s)
		}
	}
	return victims
}

// reapIdle kill 空闲会话的 opencode 进程以回收端口。端口/会话 map 的回收交给 Ensure 的 cmd.Wait
// 清理 goroutine（保持"端口仅在进程真正退出后释放"不变量，避免新会话复用未死进程端口撞 EADDRINUSE）。
// 测试用 &exec.Cmd{} 占位的会话 cmd.Process==nil，此处守 nil 不 kill。
func (m *Manager) reapIdle(now time.Time) int {
	victims := m.idleVictims(now)
	for _, s := range victims {
		if s.cmd != nil && s.cmd.Process != nil {
			log.Printf("[codews] idle-evict 回收空闲工作台: app=%s user=%s idle=%s port=%d", s.AppID, s.UserID, now.Sub(s.lastUsed).Round(time.Minute), s.Port)
			_ = s.cmd.Process.Kill()
		}
	}
	return len(victims)
}

// Start 启动后台空闲会话驱逐（reaper）。生产装配调一次（appdeploy.Register 构造 Manager 后）；
// 测试不调，避免 goroutine 泄漏。每 10 分钟扫一次，kill 超 idleTimeout 未活动的会话以回收端口。
// 注：opencode 对话原文由 performance 经 ReaderFor 直读 opencode.db（实时、进程死后仍在），无需此处搬运。
func (m *Manager) Start() {
	go func() {
		t := time.NewTicker(10 * time.Minute)
		defer t.Stop()
		for range t.C {
			if n := m.reapIdle(time.Now()); n > 0 {
				log.Printf("[codews] reaper 本轮驱逐 %d 个空闲工作台", n)
			}
		}
	}()
}
