package codews

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestInitSession 预创建会话: POST /session 返回的 id 被正确解析。
func TestInitSession(t *testing.T) {
	var gotMethod, gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		buf := make([]byte, 64)
		n, _ := r.Body.Read(buf)
		gotBody = string(buf[:n])
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"ses_abc","projectID":"p1","directory":"/data/repos/snake"}`)
	}))
	defer srv.Close()
	port := portOf(t, srv.URL)

	id := initSession(port, "")
	if gotMethod != "POST" || gotPath != "/session" {
		t.Errorf("请求 = %s %s, want POST /session", gotMethod, gotPath)
	}
	if gotBody != "{}" {
		t.Errorf("body = %q, want {}", gotBody)
	}
	if id != "ses_abc" {
		t.Errorf("initSession = %q, want ses_abc", id)
	}
}

// TestInitSessionModelRef 传 modelRef="providerID/modelID" 时，POST /session body 带
// {"model":{"providerID","id"}}，让 opencode 用授权模型而非内置免费默认模型。
// 空串仍发 {}（TestInitSession 已覆盖）。
func TestInitSessionModelRef(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 256)
		n, _ := r.Body.Read(buf)
		gotBody = string(buf[:n])
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"ses_xyz"}`)
	}))
	defer srv.Close()
	port := portOf(t, srv.URL)

	id := initSession(port, "maas/hy3")
	if id != "ses_xyz" {
		t.Fatalf("initSession = %q, want ses_xyz", id)
	}
	if !strings.Contains(gotBody, `"providerID":"maas"`) || !strings.Contains(gotBody, `"id":"hy3"`) {
		t.Errorf("modelRef 非空时 body 应含 model ref 对象, got %q", gotBody)
	}
}

// TestInitSessionRetryTransient serve 刚 listen 但 API 未就绪时重试,最终拿到 id。
func TestInitSessionRetryTransient(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits < 2 {
			http.Error(w, "not ready", 500)
			return
		}
		fmt.Fprint(w, `{"id":"ses_after_retry"}`)
	}))
	defer srv.Close()
	port := portOf(t, srv.URL)

	id := initSession(port, "")
	if id != "ses_after_retry" {
		t.Errorf("initSession = %q, want ses_after_retry(应重试后成功)", id)
	}
	if hits < 2 {
		t.Errorf("应至少重试一次, hits=%d", hits)
	}
}

// TestInitSessionFailure serve 持续失败 → 返回空串(非致命,不 panic)。
func TestInitSessionFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", 500)
	}))
	defer srv.Close()
	port := portOf(t, srv.URL)
	if id := initSession(port, ""); id != "" {
		t.Errorf("持续失败时应返回空串, got %q", id)
	}
}

// TestInitSessionUnreachable 端口无服务 → 返回空串(非致命)。
func TestInitSessionUnreachable(t *testing.T) {
	if id := initSession(1, ""); id != "" {
		t.Errorf("不可达端口应返回空串, got %q", id)
	}
}

// TestSessionDeepURL 深链接 slug = base64url(repoDir) 无 padding,
// 与 opencode web UI 的 bn(worktree) 一致, 使打开即进预创建会话。
func TestSessionDeepURL(t *testing.T) {
	b64 := func(s string) string {
		return strings.TrimRight(base64.URLEncoding.EncodeToString([]byte(s)), "=")
	}
	cases := []struct{ base, repo, sid string }{
		{"http://10.10.0.28:9400", "/data/repos/snake", "ses_abc"},
		{"http://h:9401", "/data/repos/待办管理", "ses_xy"}, // 含中文, 验证 UTF-8 编码
	}
	for _, c := range cases {
		want := fmt.Sprintf("%s/%s/session/%s", c.base, b64(c.repo), c.sid)
		if got := sessionDeepURL(c.base, c.repo, c.sid); got != want {
			t.Errorf("sessionDeepURL(%q,%q,%q)\n got %q\nwant %q", c.base, c.repo, c.sid, got, want)
		}
	}
	// 固定锚定值, 防 base64 算法漂移(与 opencode bn("/data/repos/snake") 对齐)。
	if g := sessionDeepURL("http://10.10.0.28:9400", "/data/repos/snake", "ses_abc"); g !=
		"http://10.10.0.28:9400/L2RhdGEvcmVwb3Mvc25ha2U/session/ses_abc" {
		t.Errorf("锚定值不符: %q", g)
	}
}

func portOf(t *testing.T, url string) int {
	t.Helper()
	port, err := strconv.Atoi(strings.TrimPrefix(url, "http://127.0.0.1:"))
	if err != nil {
		t.Fatalf("解析 httptest 端口失败: %v (%s)", err, url)
	}
	return port
}

// ============================================================
// per-user 模型注入：xdgConfigDir / buildOpenCodeEnv / buildClaudeEnv（纯函数）
// + buildEnv（opencode 写 per-user config，用 stub writer，不依赖 PG）
// ============================================================

// stubModelWriter ModelConfigWriter 的测试桩：记录入参，按预设返回。
// opencode 路径不触达 m.cfg，故无需 DB 即可测 buildEnv 的 XDG 注入主线。
type stubModelWriter struct {
	writeErr error // WriteOpenCodeConfigForModels 返回的错误

	gotModelIDs []string // 捕获传入的 modelIDs
	gotPath     string   // 捕获传入的 path

	name    string // ModelName 返回值
	nameErr error  // ModelName 返回的错误

	gotNameID string // 捕获传入的 modelID

	ref    string // ResolveOpencodeModelID 返回值（"providerID/modelID"）
	refErr error  // ResolveOpencodeModelID 返回的错误

	gotRefID string // 捕获传入的 modelID
}

var _ ModelConfigWriter = (*stubModelWriter)(nil)

func (s *stubModelWriter) WriteOpenCodeConfigForModels(_ context.Context, modelIDs []string, path string) error {
	s.gotModelIDs = modelIDs
	s.gotPath = path
	return s.writeErr
}

func (s *stubModelWriter) ModelName(_ context.Context, modelID string) (string, error) {
	s.gotNameID = modelID
	return s.name, s.nameErr
}

func (s *stubModelWriter) ResolveOpencodeModelID(_ context.Context, modelID string) (string, error) {
	s.gotRefID = modelID
	return s.ref, s.refErr
}

// TestXDGConfigDir per-(app,user) XDG 目录推导：稳定可复算 + sanitizeID 清洗。
// sanitize 复用 sanitizeID（与 worktree 分支名同算法），确保文件系统安全且跨次一致。
func TestXDGConfigDir(t *testing.T) {
	base := "/root/.cache/anp-codews"
	got := xdgConfigDir(base, "app1", "alice")
	if want := filepath.Join(base, "app1-alice"); got != want {
		t.Errorf("xdgConfigDir(%q,%q,%q) = %q, want %q", base, "app1", "alice", got, want)
	}
	// sanitize + 确定性：appID/userID 经 sanitizeID 后拼接，同输入两次结果一致。
	for _, c := range []struct{ app, user string }{
		{"App.One", "Bob"}, // 大写/点 → -
		{"a_b", "c@d"},     // _ / @ → -
		{"中文", "u-1"},      // 非 ascii → -（与 sanitizeID 既有行为一致）
	} {
		got := xdgConfigDir(base, c.app, c.user)
		want := filepath.Join(base, sanitizeID(c.app)+"-"+sanitizeID(c.user))
		if got != want {
			t.Errorf("xdgConfigDir sanitize: app=%q user=%q\n got %q\nwant %q", c.app, c.user, got, want)
		}
		// 稳定性：再次调用结果完全相同（可复算 → 进程重启后命中同一目录）。
		if got2 := xdgConfigDir(base, c.app, c.user); got2 != got {
			t.Errorf("xdgConfigDir 不稳定: 第一次 %q 第二次 %q", got, got2)
		}
	}
}

// TestXDGConfigBase_DefaultAndOverride 默认取 codewsXDGBase；CODEWS_XDG_BASE env 可覆盖。
func TestXDGConfigBase_DefaultAndOverride(t *testing.T) {
	if got := xdgConfigBase(); got != codewsXDGBase {
		t.Errorf("无 env 时默认 base = %q, want %q", got, codewsXDGBase)
	}
	t.Setenv("CODEWS_XDG_BASE", "/tmp/custom")
	if got := xdgConfigBase(); got != "/tmp/custom" {
		t.Errorf("env 覆盖后 base = %q, want /tmp/custom", got)
	}
}

// TestBuildOpenCodeEnv dir 非空 → 注入 XDG_CONFIG_HOME=<dir>；dir 空 → nil（兜底全局 config）。
func TestBuildOpenCodeEnv(t *testing.T) {
	dir := "/root/.cache/anp-codews/app1-alice"
	env := buildOpenCodeEnv(dir)
	if len(env) != 1 || env[0] != "XDG_CONFIG_HOME="+dir {
		t.Errorf("buildOpenCodeEnv(%q) = %v, want [XDG_CONFIG_HOME=%s]", dir, env, dir)
	}
	if env := buildOpenCodeEnv(""); env != nil {
		t.Errorf("buildOpenCodeEnv(\"\") 应返回 nil（兜底全局 config）, got %v", env)
	}
}

// TestBuildClaudeEnv 3 个 ANTHROPIC_* 变量，ANTHROPIC_MODEL 取传入 model 名。
func TestBuildClaudeEnv(t *testing.T) {
	env := buildClaudeEnv("https://x.example", "secret-key", "glm-4.6")
	want := []string{
		"ANTHROPIC_BASE_URL=https://x.example",
		"ANTHROPIC_AUTH_TOKEN=secret-key",
		"ANTHROPIC_MODEL=glm-4.6",
	}
	if len(env) != len(want) {
		t.Fatalf("buildClaudeEnv 项数 = %d, want %d (%v)", len(env), len(want), env)
	}
	for _, w := range want {
		found := false
		for _, e := range env {
			if e == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("buildClaudeEnv 缺 %q, got %v", w, env)
		}
	}
}

// TestBuildEnv_OpenCodeWritesPerUserConfig opencode + 授权模型 + writer →
// 写 per-user config 到 XDG 目录，env 注入 XDG_CONFIG_HOME 指向该目录。
func TestBuildEnv_OpenCodeWritesPerUserConfig(t *testing.T) {
	base := t.TempDir()
	t.Setenv("CODEWS_XDG_BASE", base)
	w := &stubModelWriter{}
	m := NewManager("h", nil)
	m.SetModelAccess(w)

	env := m.buildEnv(context.Background(), "opencode", "cmd_123", "app1", "alice")

	if !sliceEqual(w.gotModelIDs, []string{"cmd_123"}) {
		t.Errorf("writer 收到 modelIDs = %v, want [cmd_123]", w.gotModelIDs)
	}
	wantDir := xdgConfigDir(base, "app1", "alice")
	wantPath := filepath.Join(wantDir, "opencode", "opencode.json")
	if w.gotPath != wantPath {
		t.Errorf("writer 收到 path = %q, want %q", w.gotPath, wantPath)
	}
	found := false
	for _, e := range env {
		if e == "XDG_CONFIG_HOME="+wantDir {
			found = true
		}
	}
	if !found {
		t.Errorf("env 缺 XDG_CONFIG_HOME=%s: %v", wantDir, env)
	}
}

// TestBuildEnv_OpenCodeWriteFailsFallsBack 写 per-user config 失败 → 降级返 nil（用全局 config），
// 不阻断会话（仅 log warn）。
func TestBuildEnv_OpenCodeWriteFailsFallsBack(t *testing.T) {
	t.Setenv("CODEWS_XDG_BASE", t.TempDir())
	w := &stubModelWriter{writeErr: errors.New("db down")}
	m := NewManager("h", nil)
	m.SetModelAccess(w)

	if env := m.buildEnv(context.Background(), "opencode", "cmd_1", "app", "u"); env != nil {
		t.Errorf("写失败应降级返回 nil（用全局 config）, got %v", env)
	}
}

// TestBuildEnv_OpenCodeNoModelReturnsNil opencode + 空 model → 兜底全局 config，env=nil。
func TestBuildEnv_OpenCodeNoModelReturnsNil(t *testing.T) {
	m := NewManager("h", nil)
	m.SetModelAccess(&stubModelWriter{})
	if env := m.buildEnv(context.Background(), "opencode", "", "app", "u"); env != nil {
		t.Errorf("空 model 应兜底全局 config 返回 nil, got %v", env)
	}
}

// TestBuildEnv_OpenCodeNoWriterReturnsNil opencode + writer 未注入（nil）→ 兜底全局 config。
func TestBuildEnv_OpenCodeNoWriterReturnsNil(t *testing.T) {
	m := NewManager("h", nil) // writer 未注入（渐进迁移：未接 computeStore 的部署）
	if env := m.buildEnv(context.Background(), "opencode", "cmd_1", "app", "u"); env != nil {
		t.Errorf("writer 未注入应兜底全局 config 返回 nil, got %v", env)
	}
}

// TestBuildEnv_UnknownToolReturnsNil 未识别工具 → nil（继承 os.Environ）。
func TestBuildEnv_UnknownToolReturnsNil(t *testing.T) {
	m := NewManager("h", nil)
	m.SetModelAccess(&stubModelWriter{})
	if env := m.buildEnv(context.Background(), "codex", "cmd_1", "app", "u"); env != nil {
		t.Errorf("未知工具应返回 nil, got %v", env)
	}
}

// TestSetModelAccess 注入 writer 后字段非 nil；构造时默认 nil。
func TestSetModelAccess(t *testing.T) {
	m := NewManager("h", nil)
	if m.writer != nil {
		t.Error("NewManager 后 writer 应为 nil（兜底全局 config）")
	}
	w := &stubModelWriter{}
	m.SetModelAccess(w)
	if m.writer != w {
		t.Errorf("SetModelAccess 后 m.writer = %p, want %p", m.writer, w)
	}
}

// sliceEqual 简单字符串切片相等比较（避免在测试里引 slices 仅此一处）。
func sliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
