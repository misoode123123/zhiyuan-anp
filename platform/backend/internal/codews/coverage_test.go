package codews

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// 本文件为补齐 codews 包覆盖率新增的测试集合，聚焦可测的纯逻辑与 HTTP 边界。
// 进程真实启停(exec.Command 跑 opencode serve)、文件系统真实写入已尽量避开；
// 必须涉及进程/路径的，用 &exec.Cmd{} 占位（ProcessState 默认 nil → alive()=true）
// 或 t.TempDir() 走"已存在/不可达"分支，不真实执行外部命令。

// ============================================================
// sanitizeID
// ============================================================

// TestSanitizeID 把 userID 转为 git 友好分支/目录名（小写字母数字保留, 其他→-）。
// 覆盖：纯字母数字、大写→小写、特殊字符(_ . @ 空格 中文)→-、空输入回退 dev。
func TestSanitizeID(t *testing.T) {
	cases := []struct{ in, want string }{
		{"alice", "alice"},
		{"Alice", "alice"},                 // 大写 → 小写
		{"alice_bob", "alice-bob"},         // _ → -
		{"alice.bob", "alice-bob"},         // . → -
		{"alice@bob.com", "alice-bob-com"}, // @ 和 . → -
		{"user-01", "user-01"},             // 已规范的 - 和数字保留
		{"alice bob", "alice-bob"},         // 空格 → -
		{"中文", "--"},                       // 中文每个 rune → 一个 -（range 字符串按 rune 迭代）
		{"", "dev"},                        // 空输入 → "dev"（防 git 拒绝空分支名）
	}
	for _, c := range cases {
		if got := sanitizeID(c.in); got != c.want {
			t.Errorf("sanitizeID(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// ============================================================
// NewManager / Register / Tools（构造与注册的纯逻辑）
// ============================================================

// stubTool 测试用 Tool 实现：Name 返回注入值，Start 不实际启动进程。
type stubTool struct{ name string }

func (s stubTool) Name() string { return s.name }
func (s stubTool) Start(string, int, []string) (*exec.Cmd, error) {
	return nil, fmt.Errorf("stub 不启动")
}

// TestNewManager_DefaultTools NewManager 应预注册 opencode/claude/codex 三工具。
func TestNewManager_DefaultTools(t *testing.T) {
	m := NewManager("127.0.0.1", nil)
	got := append([]string(nil), m.Tools()...)
	sort.Strings(got)
	want := []string{"claude", "codex", "opencode"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("默认工具 = %v, want %v", got, want)
	}
}

// TestNewManager_HostPropagated host 字段应存到 Manager，影响后续 Session.URL 推算。
func TestNewManager_HostPropagated(t *testing.T) {
	m := NewManager("10.10.0.28", nil)
	if m.host != "10.10.0.28" {
		t.Errorf("host = %q, want 10.10.0.28", m.host)
	}
}

// TestRegister_AddsTool Register 后工具应出现在 Tools() 列表中。
func TestRegister_AddsTool(t *testing.T) {
	m := NewManager("h", nil)
	m.Register(stubTool{name: "custom"})
	found := false
	for _, n := range m.Tools() {
		if n == "custom" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Register 后未在 Tools 中找到 'custom'")
	}
}

// TestRegister_Overwrite 同名工具再次 Register 应覆盖（数量不变, 取最新）。
func TestRegister_Overwrite(t *testing.T) {
	m := NewManager("h", nil)
	m.Register(stubTool{name: "opencode"})
	if n := len(m.Tools()); n != 3 {
		t.Errorf("同名覆盖后工具数 = %d, want 3", n)
	}
}

// ============================================================
// Get（map 查找的纯逻辑分支）
// ============================================================

// TestGet_Empty 空 Manager.Get 必返回 nil。
func TestGet_Empty(t *testing.T) {
	m := NewManager("h", nil)
	if s := m.Get("app", "user"); s != nil {
		t.Errorf("空 Manager.Get 应返回 nil, got %+v", s)
	}
}

// TestGet_DeadSession Session 无 cmd（已退出或未启动）→ alive()=false → Get 返回 nil。
func TestGet_DeadSession(t *testing.T) {
	m := NewManager("h", nil)
	m.sessions["app:user"] = &Session{AppID: "app", UserID: "user"}
	if s := m.Get("app", "user"); s != nil {
		t.Errorf("无 cmd 的 Session 应视为已死, got %+v", s)
	}
}

// TestGet_AliveSession cmd 非 nil 且 ProcessState==nil → 返回该 Session 实例。
func TestGet_AliveSession(t *testing.T) {
	m := NewManager("h", nil)
	injected := &Session{
		AppID: "app", UserID: "user",
		cmd: &exec.Cmd{}, // ProcessState 默认 nil → alive()=true
	}
	m.sessions["app:user"] = injected
	if got := m.Get("app", "user"); got != injected {
		t.Errorf("alive Session 应返回注入实例, got %+v", got)
	}
}

// TestGet_KeyAppUserID Get 用 "appID:userID" 拼接 key 查找；混淆 appID/userID 不应命中。
func TestGet_KeyAppUserID(t *testing.T) {
	m := NewManager("h", nil)
	injected := &Session{AppID: "app", UserID: "u", cmd: &exec.Cmd{}}
	m.sessions["app:u"] = injected
	// 反着拼成 "u:app" 不应命中
	if s := m.Get("u", "app"); s != nil {
		t.Errorf("key 拼接顺序错位时不应命中, got %+v", s)
	}
	if s := m.Get("app", "u"); s != injected {
		t.Errorf("正确 key 应命中注入实例, got %+v", s)
	}
}

// ============================================================
// alive
// ============================================================

// TestAlive_NilCases nil *Session / 空 Session 不应 panic, 均返回 false。
func TestAlive_NilCases(t *testing.T) {
	var nilS *Session
	if nilS.alive() {
		t.Error("nil *Session.alive() 应 false（不应 panic）")
	}
	emptyS := &Session{}
	if emptyS.alive() {
		t.Error("空 Session.alive() 应 false (cmd==nil)")
	}
}

// TestAlive_ProcessStateSet cmd 已退出（ProcessState 非 nil）→ false。
// 启动一个立即退出的进程(go version), 等 Run 返回, 此时 ProcessState 已设置。
func TestAlive_ProcessStateSet(t *testing.T) {
	cmd := exec.Command("go", "version")
	if err := cmd.Run(); err != nil {
		t.Fatalf("go version 失败: %v", err)
	}
	s := &Session{cmd: cmd}
	if s.alive() {
		t.Error("已退出进程(ProcessState 非 nil) alive() 应 false")
	}
}

// ============================================================
// allocPortLocked
// ============================================================

// TestAllocPortLocked_Empty 空会话应分配首个端口 portMin。
func TestAllocPortLocked_Empty(t *testing.T) {
	m := NewManager("h", nil)
	if p := m.allocPortLocked(); p != portMin {
		t.Errorf("空 Manager 首 port = %d, want %d", p, portMin)
	}
}

// TestAllocPortLocked_SkipUsed 已占用端口应被跳过, 返回最小可用。
// 端口占用经端口注册表 m.ports 标记（F-1：端口生命周期独立于 sessions，
// allocPort 即刻登记，进程 cmd.Wait 后由清理 goroutine 释放）。
func TestAllocPortLocked_SkipUsed(t *testing.T) {
	m := NewManager("h", nil)
	m.ports[portMin] = true
	m.ports[portMin+2] = true
	if got := m.allocPortLocked(); got != portMin+1 {
		t.Errorf("占用 %d/%d 后应分配 %d, got %d", portMin, portMin+2, portMin+1, got)
	}
}

// TestAllocPortLocked_Full 所有端口(9400-9450)都占用 → 返回 0（无可用）。
func TestAllocPortLocked_Full(t *testing.T) {
	m := NewManager("h", nil)
	for p := portMin; p <= portMax; p++ {
		m.ports[p] = true
	}
	if p := m.allocPortLocked(); p != 0 {
		t.Errorf("所有端口占用应返回 0, got %d", p)
	}
}

// ============================================================
// waitListen
// ============================================================

// TestWaitListen_Reachable 已 listen 的端口应快速返回 true。
func TestWaitListen_Reachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	if !waitListen(portOf(t, srv.URL), 2*time.Second) {
		t.Error("httptest 端口应可达")
	}
}

// TestWaitListen_Unreachable 不可达端口 + 短超时应返回 false（且不阻塞过久）。
func TestWaitListen_Unreachable(t *testing.T) {
	start := time.Now()
	if waitListen(1, 300*time.Millisecond) {
		t.Error("不可达端口(1) 不应返回 true")
	}
	// 防回归到无限阻塞：大致遵守超时（容许少量轮询溢出, <3s 即可）
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("waitListen 应遵守超时, 实际耗时 %v", d)
	}
}

// ============================================================
// ensureSession（HTTP 边界, 不真实启 opencode serve）
// ============================================================

// TestEnsureSession_PicksMatchingNewest /api/session 返回多条同 repoDir 会话,
// 应挑选 Time.Updated 最大的（最近活跃的那个）。
func TestEnsureSession_PicksMatchingNewest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":[
            {"id":"old","time":{"updated":1000},"location":{"directory":"/r"}},
            {"id":"new","time":{"updated":5000},"location":{"directory":"/r"}},
            {"id":"other","time":{"updated":9000},"location":{"directory":"/other"}}
        ]}`)
	}))
	defer srv.Close()
	if got := ensureSession(portOf(t, srv.URL), "/r", false, ""); got != "new" {
		t.Errorf("ensureSession 应选 updated 最大的匹配项 new, got %q", got)
	}
}

// TestEnsureSession_NoMatchCallsInit /api/session 无匹配 directory → 走 initSession 新建。
func TestEnsureSession_NoMatchCallsInit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/session" {
			fmt.Fprint(w, `{"data":[{"id":"x","time":{"updated":1},"location":{"directory":"/other"}}]}`)
			return
		}
		fmt.Fprint(w, `{"id":"fresh"}`)
	}))
	defer srv.Close()
	if got := ensureSession(portOf(t, srv.URL), "/r", false, ""); got != "fresh" {
		t.Errorf("无匹配应调 initSession 返回 fresh, got %q", got)
	}
}

// TestEnsureSession_EmptyList 空会话列表 → 同样走 initSession 分支。
func TestEnsureSession_EmptyList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/session" {
			fmt.Fprint(w, `{"data":[]}`)
			return
		}
		fmt.Fprint(w, `{"id":"empty_new"}`)
	}))
	defer srv.Close()
	if got := ensureSession(portOf(t, srv.URL), "/r", false, ""); got != "empty_new" {
		t.Errorf("空列表应走 initSession 返回 empty_new, got %q", got)
	}
}

// TestEnsureSession_ForceNewBypassesReuse forceNew=true 时即使 /api/session 有匹配的最近会话,
// 也应跳过复用直接 initSession 新建（换需求时不把旧需求会话捞回）。
func TestEnsureSession_ForceNewBypassesReuse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/session" {
			fmt.Fprint(w, `{"data":[{"id":"reuse_me","time":{"updated":9000},"location":{"directory":"/r"}}]}`)
			return
		}
		// /session (initSession)
		fmt.Fprint(w, `{"id":"fresh"}`)
	}))
	defer srv.Close()
	if got := ensureSession(portOf(t, srv.URL), "/r", true, ""); got != "fresh" {
		t.Errorf("forceNew=true 应跳过 reuse_me 走 initSession 返回 fresh, got %q", got)
	}
}

// TestEnsureSession_ForceNewFalseStillReuses forceNew=false 时保留原"取 updated 最近匹配"行为。
func TestEnsureSession_ForceNewFalseStillReuses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":[{"id":"new","time":{"updated":5000},"location":{"directory":"/r"}}]}`)
	}))
	defer srv.Close()
	if got := ensureSession(portOf(t, srv.URL), "/r", false, ""); got != "new" {
		t.Errorf("forceNew=false 应复用 new, got %q", got)
	}
}

// ============================================================
// shouldForceNewForRequirement（换需求判定纯函数）
// ============================================================

// TestShouldForceNewForRequirement 换需求(或旧会话无需求绑定而现在选了需求)→true;
// 首次(nil)/没选需求(reqID空)/同需求 →false。
func TestShouldForceNewForRequirement(t *testing.T) {
	cases := []struct {
		name string
		old  *Session
		req  string
		want bool
	}{
		{"首次无旧会话", nil, "r1", false},
		{"没选需求", &Session{RequirementID: "r1"}, "", false},
		{"同需求", &Session{RequirementID: "r1"}, "r1", false},
		{"换需求", &Session{RequirementID: "r1"}, "r2", true},
		{"旧会话无需求现在选了", &Session{RequirementID: ""}, "r1", true},
	}
	for _, c := range cases {
		if got := shouldForceNewForRequirement(c.old, c.req); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

// ============================================================
// computeForceNew（请求级 + 热切换 + 冷启动跨需求 综合判定纯函数）
// ============================================================

// TestComputeForceNew 综合判定是否强制新建 opencode 会话（跳过磁盘复用直接 initSession）。
// 覆盖：① 请求级显式 reqForceNew；② 内存热会话换需求；③ 冷启动(old==nil)库最近会话跨需求。
func TestComputeForceNew(t *testing.T) {
	warm := &Session{RequirementID: "reqA"} // old != nil
	cases := []struct {
		name        string
		old         *Session
		reqID       string
		reqForceNew bool
		last        *SessionRecord
		want        bool
	}{
		{"请求级强制", nil, "reqA", true, nil, true},
		{"热切换需求", warm, "reqB", false, nil, true},
		{"热同需求不强制", warm, "reqA", false, nil, false},
		{"热无需求不强制", warm, "", false, nil, false},
		{"冷启动跨需求", nil, "reqB", false, &SessionRecord{RequirementID: "reqA"}, true},
		{"冷启动同需求不强制", nil, "reqA", false, &SessionRecord{RequirementID: "reqA"}, false},
		{"冷启动无历史不强制", nil, "reqA", false, nil, false},
		{"冷启动无需求不强制", nil, "", false, &SessionRecord{RequirementID: "reqA"}, false},
		{"冷启动历史无req不强制", nil, "reqB", false, &SessionRecord{RequirementID: ""}, false},
	}
	for _, c := range cases {
		if got := computeForceNew(c.old, c.reqID, c.reqForceNew, c.last); got != c.want {
			t.Errorf("%s: want %v got %v", c.name, c.want, got)
		}
	}
}

// ============================================================
// SessionMessages（HTTP 拉取 + 纯解析拼装）
// ============================================================

// TestSessionMessages_NoSession 无活跃会话 → 返回 ("", nil)（非致命）。
func TestSessionMessages_NoSession(t *testing.T) {
	m := NewManager("h", nil)
	out, err := m.SessionMessages("app", "user")
	if err != nil || out != "" {
		t.Errorf("无会话应返回 ('',nil), got (%q,%v)", out, err)
	}
}

// TestSessionMessages_NoSessionID 会话 alive 但无 SessionID → 返回 ("", nil)。
func TestSessionMessages_NoSessionID(t *testing.T) {
	m := NewManager("h", nil)
	m.sessions["app:user"] = &Session{
		AppID: "app", UserID: "user",
		cmd: &exec.Cmd{}, // alive, 但 SessionID 为空
	}
	out, err := m.SessionMessages("app", "user")
	if err != nil || out != "" {
		t.Errorf("无 SessionID 应返回 ('',nil), got (%q,%v)", out, err)
	}
}

// TestSessionMessages_ParsesAndFormats 拉 /api/session/<id>/message,
// 仅保留 type=text 且非空白的 part, 拼成 "[role] text\n", TrimSpace 收尾。
func TestSessionMessages_ParsesAndFormats(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":[
            {"type":"user","parts":[{"type":"text","text":"hello"}]},
            {"type":"assistant","parts":[{"type":"text","text":"hi there"}]},
            {"type":"tool","parts":[{"type":"json","text":"..."}]},
            {"type":"user","parts":[{"type":"text","text":"   "}]}
        ]}`)
	}))
	defer srv.Close()
	port := portOf(t, srv.URL)

	m := NewManager("h", nil)
	m.sessions["app:user"] = &Session{
		AppID: "app", UserID: "user", Port: port,
		SessionID: "ses_1",
		cmd:       &exec.Cmd{},
	}
	got, err := m.SessionMessages("app", "user")
	if err != nil {
		t.Fatalf("SessionMessages 错误: %v", err)
	}
	want := "[user] hello\n[assistant] hi there"
	if got != want {
		t.Errorf("SessionMessages 输出不符:\n got %q\nwant %q", got, want)
	}
}

// TestSessionMessages_DecodeFails /api/session/.../message 返回非 JSON,
// decode 失败 → 返回 error（不静默吞）。
func TestSessionMessages_DecodeFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `not json`)
	}))
	defer srv.Close()
	port := portOf(t, srv.URL)
	m := NewManager("h", nil)
	m.sessions["app:user"] = &Session{
		AppID: "app", UserID: "user", Port: port,
		SessionID: "ses_1",
		cmd:       &exec.Cmd{},
	}
	if _, err := m.SessionMessages("app", "user"); err == nil {
		t.Error("JSON 解析失败应返回 error")
	}
}

// TestSessionMessages_HTTPFetchFails 工作台不可达(Port=1) → GET 失败 → 返回 error。
func TestSessionMessages_HTTPFetchFails(t *testing.T) {
	m := NewManager("h", nil)
	m.sessions["app:user"] = &Session{
		AppID: "app", UserID: "user", Port: 1, // 不可达
		SessionID: "ses_1",
		cmd:       &exec.Cmd{},
	}
	if _, err := m.SessionMessages("app", "user"); err == nil {
		t.Error("工作台不可达时 GET 失败应返回 error")
	}
}

// ============================================================
// SendPrompt（HTTP POST 边界）
// ============================================================

// TestSendPrompt_NoSession 无活跃会话 → 返回"无活跃编码会话"错误。
func TestSendPrompt_NoSession(t *testing.T) {
	m := NewManager("h", nil)
	err := m.SendPrompt("app", "user", "do something")
	if err == nil {
		t.Fatal("无会话应返回错误")
	}
	if !strings.Contains(err.Error(), "无活跃编码会话") {
		t.Errorf("错误信息应含'无活跃编码会话', got %q", err.Error())
	}
}

// TestSendPrompt_PostsPrompt 向 /session/<id>/prompt_async 发 POST（opencode web SPA 同款），
// body 形如 {"messageID":...,"parts":[{"type":"text","text":...}]}；2xx → nil。
// 旧实现用 /api/session/<id>/prompt {prompt:{text}}——该端点虽 admitted 但实测不落任何消息，
// 会话深链打开即空白（headless 探针 probe9 Flow A 证实），故改用 prompt_async。
func TestSendPrompt_PostsPrompt(t *testing.T) {
	var gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
	}))
	defer srv.Close()
	port := portOf(t, srv.URL)

	m := NewManager("h", nil)
	m.sessions["app:user"] = &Session{
		AppID: "app", UserID: "user", Port: port,
		SessionID: "ses_1",
		cmd:       &exec.Cmd{},
	}
	if err := m.SendPrompt("app", "user", "implement feature X"); err != nil {
		t.Fatalf("SendPrompt 错误: %v", err)
	}
	wantPath := "/session/ses_1/prompt_async"
	if gotPath != wantPath {
		t.Errorf("请求路径 = %q, want %q（旧 /api/session/<id>/prompt 不落消息→空白）", gotPath, wantPath)
	}
	var got struct {
		MessageID string `json:"messageID"`
		Parts     []struct {
			ID   string `json:"id"`
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"parts"`
	}
	if err := json.Unmarshal([]byte(gotBody), &got); err != nil {
		t.Fatalf("body 不是合法 JSON: %v (%s)", err, gotBody)
	}
	if got.MessageID == "" {
		t.Errorf("body 缺 messageID: %s", gotBody)
	}
	if len(got.Parts) != 1 || got.Parts[0].Type != "text" || got.Parts[0].Text != "implement feature X" {
		t.Errorf("parts 应为 [{type:text,text:\"implement feature X\"}], got %+v (%s)", got.Parts, gotBody)
	}
}

// TestSendPrompt_HTTPError 工作台不可达 → 返回 error（不静默吞）。
func TestSendPrompt_HTTPError(t *testing.T) {
	m := NewManager("h", nil)
	m.sessions["app:user"] = &Session{
		AppID: "app", UserID: "user", Port: 1, // 不可达
		SessionID: "ses_1",
		cmd:       &exec.Cmd{},
	}
	if err := m.SendPrompt("app", "user", "x"); err == nil {
		t.Error("工作台不可达应返回 error")
	}
}

// ============================================================
// Ensure：仅测不真实启动进程的错误/复用路径
// ============================================================

// TestEnsure_UnknownTool 未注册的工具名 → 立即返回错误, 不启动进程。
func TestEnsure_UnknownTool(t *testing.T) {
	m := NewManager("h", nil)
	_, err := m.Ensure("ps_1", "app", "/tmp/repo", "u", "no-such-tool", "", "", false)
	if err == nil {
		t.Fatal("未知工具应返回错误")
	}
	if !strings.Contains(err.Error(), "未知编码工具") {
		t.Errorf("错误信息应含'未知编码工具', got %q", err.Error())
	}
}

// TestEnsure_PortExhausted 所有端口已占用 → 返回"无可用工作台端口"。
// 这里只测错误路径, 不实际启动 opencode。
func TestEnsure_PortExhausted(t *testing.T) {
	m := NewManager("h", nil)
	for p := portMin; p <= portMax; p++ {
		m.ports[p] = true // 经端口注册表占满（F-1）
	}
	_, err := m.Ensure("ps_1", "app", "/tmp/repo", "u", "opencode", "", "", false)
	if err == nil {
		t.Fatal("端口耗尽应返回错误")
	}
	if !strings.Contains(err.Error(), "无可用工作台端口") {
		t.Errorf("错误信息应含'无可用工作台端口', got %q", err.Error())
	}
}

// TestEnsure_ReuseAliveSameTool 同 appID+userID+tool 的 alive 会话 → 复用, 不重启。
func TestEnsure_ReuseAliveSameTool(t *testing.T) {
	m := NewManager("h", nil)
	existing := &Session{
		AppID: "app", UserID: "u", Tool: "opencode",
		Port: 9400,
		cmd:  &exec.Cmd{}, // alive
	}
	m.sessions["app:u"] = existing
	got, err := m.Ensure("ps_1", "app", "/tmp/repo", "u", "opencode", "", "", false)
	if err != nil {
		t.Fatalf("Ensure 复用错误: %v", err)
	}
	if got != existing {
		t.Errorf("Ensure 应复用现有 alive 会话, got %+v", got)
	}
}

// TestEnsure_DefaultUserID userID 空 → 默认 "anonymous", 用此 key 命中复用。
func TestEnsure_DefaultUserID(t *testing.T) {
	m := NewManager("h", nil)
	existing := &Session{
		AppID: "app", UserID: "anonymous", Tool: "opencode",
		cmd: &exec.Cmd{},
	}
	m.sessions["app:anonymous"] = existing
	got, err := m.Ensure("ps_1", "app", "/tmp/repo", "", "opencode", "", "", false)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if got != existing {
		t.Errorf("空 userID 应默认 anonymous 复用现有会话, got %+v", got)
	}
}

// TestEnsure_DefaultToolName toolName 空 → 默认 "opencode"。
func TestEnsure_DefaultToolName(t *testing.T) {
	m := NewManager("h", nil)
	existing := &Session{
		AppID: "app", UserID: "u", Tool: "opencode",
		cmd: &exec.Cmd{},
	}
	m.sessions["app:u"] = existing
	got, err := m.Ensure("ps_1", "app", "/tmp/repo", "u", "", "", "", false)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if got != existing {
		t.Errorf("空 toolName 应默认 opencode 复用, got %+v", got)
	}
}

// TestEnsure_ReuseSameRequirement 已绑定 reqA 的 alive 会话，Ensure 同 reqA → 复用。
func TestEnsure_ReuseSameRequirement(t *testing.T) {
	m := NewManager("h", nil)
	existing := &Session{
		AppID: "app", UserID: "u", Tool: "opencode",
		RequirementID: "reqA",
		cmd:           &exec.Cmd{},
	}
	m.sessions["app:u"] = existing
	got, err := m.Ensure("ps_1", "app", "/tmp/repo", "u", "opencode", "reqA", "", false)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if got != existing {
		t.Errorf("同需求 reqA 应复用现有会话, got %+v", got)
	}
}

// TestEnsure_RefreshEmptyReqReusesBound 刷新场景：reqID 空 → 沿用已绑定 reqA 的会话（不 kill，保留绑定）。
func TestEnsure_RefreshEmptyReqReusesBound(t *testing.T) {
	m := NewManager("h", nil)
	existing := &Session{
		AppID: "app", UserID: "u", Tool: "opencode",
		RequirementID: "reqA",
		cmd:           &exec.Cmd{},
	}
	m.sessions["app:u"] = existing
	got, err := m.Ensure("ps_1", "app", "/tmp/repo", "u", "opencode", "", "", false)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if got != existing {
		t.Errorf("空 reqID 刷新应沿用已绑定会话(保留 requirement_id), got %+v", got)
	}
}

// ============================================================
// ensureWorktree（路径推算 + 已存在分支, 不真实跑 git）
// ============================================================

// TestEnsureWorktree_PathConstruction 不实际跑 git, 只验路径推算:
// wt = repoDir/.worktrees/<sanitized userID>; .git 在 → 走"已存在"分支不调 git。
func TestEnsureWorktree_PathConstruction(t *testing.T) {
	repoDir := t.TempDir()
	wtDir := filepath.Join(repoDir, ".worktrees", "alice")
	if err := os.MkdirAll(filepath.Join(wtDir, ".git"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if got := ensureWorktree(repoDir, "alice"); got != wtDir {
		t.Errorf("ensureWorktree 路径 = %q, want %q", got, wtDir)
	}
}

// TestEnsureWorktree_SanitizesUserID userID 经 sanitizeID 后拼路径（"A.B" → "a-b"）。
func TestEnsureWorktree_SanitizesUserID(t *testing.T) {
	repoDir := t.TempDir()
	wtDir := filepath.Join(repoDir, ".worktrees", "a-b")
	_ = os.MkdirAll(filepath.Join(wtDir, ".git"), 0755)
	if got := ensureWorktree(repoDir, "A.B"); got != wtDir {
		t.Errorf("userID 'A.B' 应 sanitize 为 'a-b', path = %q, want %q", got, wtDir)
	}
}

// TestEnsureWorktree_NoGitRepo repoDir 非 git 仓库时 git worktree add 必失败；
// ensureWorktree 兜底回退主仓 repoDir（避免 opencode chdir 到无效 .worktrees 目录起不来）。
func TestEnsureWorktree_NoGitRepo(t *testing.T) {
	repoDir := t.TempDir()
	if got := ensureWorktree(repoDir, "Bob"); got != repoDir {
		t.Errorf("非 git 仓库应回退主仓 %q, got %q", repoDir, got)
	}
}

// ============================================================
// allocPort / 端口注册表（F-1：端口生命周期独立于 sessions，防并发/kill 后重用同端口）
// ============================================================

// TestAllocPort_ReservesAndFrees 连续分配得到递增端口且即刻登记；释放后最低空闲端口可再分配。
// 守护 F-1：端口在 allocPort 时就进注册表，避免并发 Ensure 或 kill-后-未死 时抢同端口。
func TestAllocPort_ReservesAndFrees(t *testing.T) {
	m := NewManager("h", nil)
	p1 := m.allocPortLocked()
	p2 := m.allocPortLocked()
	if p1 != portMin || p2 != portMin+1 {
		t.Errorf("前两次应分配 %d/%d, got %d/%d", portMin, portMin+1, p1, p2)
	}
	if !m.ports[p1] || !m.ports[p2] {
		t.Errorf("端口未登记为已用: ports=%v", m.ports)
	}
	// 模拟清理 goroutine 释放 p1（进程退出归还端口）
	delete(m.ports, p1)
	if p3 := m.allocPortLocked(); p3 != p1 {
		t.Errorf("释放后应重新分配最低空闲端口 %d, got %d", p1, p3)
	}
}
