package appgw

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// fakeStore 内存 RouteStore，单测注入。
type fakeStore struct {
	routes map[string]*Route // key = appCode+"\x00"+env
}

func newFakeStore(rs ...*Route) *fakeStore {
	m := map[string]*Route{}
	for _, r := range rs {
		m[r.AppCode+"\x00"+r.Env] = r
	}
	return &fakeStore{routes: m}
}

func (f *fakeStore) GetRoute(_ context.Context, code, env string) (*Route, error) {
	if r, ok := f.routes[code+"\x00"+env]; ok {
		return r, nil
	}
	return nil, nil
}

// fakeAuth TokenVerifier，token=tok_ok 视为有效用户 alice，其他无效。
type fakeAuth struct{ okUser string }

func (f *fakeAuth) ValidToken(_ context.Context, token string) (string, bool) {
	if token == "tok_ok" {
		return f.okUser, true
	}
	return "", false
}

func TestParseAppsPath(t *testing.T) {
	cases := []struct {
		in              string
		code, env, rest string
		ok              bool
	}{
		{"/apps/app_x/api/q", "app_x", "", "api/q", true},
		{"/apps/app_x~test/", "app_x", "test", "", true},
		{"/apps/app_x", "app_x", "", "", true},
		{"/apps/app_x/", "app_x", "", "", true},
		{"/apps/app_x~prod/health", "app_x", "prod", "health", true},
		{"/apps/", "", "", "", false},
		{"/apps", "", "", "", false}, // TrimPrefix 后为 ""（无尾 /）
		{"/api/v1/foo", "", "", "", false},
		{"/apps/~test/foo", "", "", "", false}, // code 空串
	}
	for _, c := range cases {
		code, env, rest, ok := parseAppsPath(c.in)
		if ok != c.ok || code != c.code || env != c.env || rest != c.rest {
			t.Errorf("parseAppsPath(%q) = (%q,%q,%q,%v) want (%q,%q,%q,%v)",
				c.in, code, env, rest, ok, c.code, c.env, c.rest, c.ok)
		}
	}
}

// startUpstream 启动一个假应用容器（返回固定文本 + 回显收到的身份头）。
// 返回 (host:port, headersSeen, close)。
func startUpstream(t *testing.T) (addr string, lastHeaders *http.Header, hit *int) {
	t.Helper()
	hdr := http.Header{}
	var n int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hdr.Set("X-User", r.Header.Get("X-User"))
		hdr.Set("X-Project-Space-Id", r.Header.Get("X-Project-Space-Id"))
		hdr.Set("X-Trace-Id", r.Header.Get("X-Trace-Id"))
		n++
		w.Header().Set("X-Upstream-Path", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "hello from upstream; path="+r.URL.Path+"; q="+r.URL.RawQuery)
	}))
	t.Cleanup(srv.Close)
	// httptest.Server.Listener.Addr().String() → 127.0.0.1:port，与 ReverseProxy target 兼容
	host, port, _ := netSplitHostPort(srv.Listener.Addr().String())
	return host + ":" + port, &hdr, &n
}

// netSplitHostPort 简单 net.SplitHostPort 包装（避免单独 import）。
func netSplitHostPort(addr string) (string, string, error) {
	i := strings.LastIndex(addr, ":")
	if i < 0 {
		return addr, "", nil
	}
	return addr[:i], addr[i+1:], nil
}

// TestGateway_ReverseProxyAuth 测三件事：
//  1. auth_required=true 不带 token → 401
//  2. auth_required=true 带 tok_ok → 反代成功 + 身份头注入 + 路径剥离前缀
//  3. 不存在的 app_code → 404
//
// 注：用 httptest.NewServer 跑真实 HTTP（非 NewRecorder），因为 ReverseProxy
// 经 gin responseWriter 会要 CloseNotifier，ResponseRecorder 没实现会 panic。
func TestGateway_ReverseProxyAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)

	addr, seenHdr, hit := startUpstream(t)
	host, port, _ := netSplitHostPort(addr)
	portI := 0
	for _, ch := range port {
		portI = portI*10 + int(ch-'0')
	}

	route := &Route{
		AppCode: "app_demo", Env: "prod",
		ProjectSpaceID: "ps_1", UpstreamHost: host, UpstreamPort: portI,
		Status: StatusActive, AuthRequired: true,
	}
	g := NewGateway(newFakeStore(route), &fakeAuth{okUser: "alice"}, nil, nil)

	r := gin.New()
	r.Any("/apps/*any", g.ReverseProxy)
	gwSrv := httptest.NewServer(r)
	t.Cleanup(gwSrv.Close)

	// Case 1: 不带 token → 401
	{
		resp, err := http.Get(gwSrv.URL + "/apps/app_demo/api/list?page=1")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("未带 token 应 401，得到 %d body=%s", resp.StatusCode, body)
		}
		if *hit != 0 {
			t.Fatalf("未鉴权不应转发到 upstream，hit=%d", *hit)
		}
	}

	// Case 2: 带 tok_ok → 反代 + 头注入 + 路径前缀剥离
	{
		req, _ := http.NewRequest("GET", gwSrv.URL+"/apps/app_demo/api/list?page=1", nil)
		req.Header.Set("Authorization", "Bearer tok_ok")
		req.Header.Set("X-Trace-Id", "trace-xyz")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("do: %v", err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("带 token 应 200，得到 %d body=%s", resp.StatusCode, body)
		}
		if *hit != 1 {
			t.Fatalf("应转发到 upstream 1 次，hit=%d", *hit)
		}
		// upstream 看到的 path 应剥离 /apps/app_demo 前缀
		if got := resp.Header.Get("X-Upstream-Path"); got != "/api/list" {
			t.Fatalf("upstream path 应为 /api/list，得到 %q", got)
		}
		if seenHdr.Get("X-User") != "alice" {
			t.Fatalf("应注入 X-User=alice，得到 %q", seenHdr.Get("X-User"))
		}
		if seenHdr.Get("X-Project-Space-Id") != "ps_1" {
			t.Fatalf("应注入 X-Project-Space-Id=ps_1，得到 %q", seenHdr.Get("X-Project-Space-Id"))
		}
		if seenHdr.Get("X-Trace-Id") != "trace-xyz" {
			t.Fatalf("应注入 X-Trace-Id=trace-xyz，得到 %q", seenHdr.Get("X-Trace-Id"))
		}
		if !strings.Contains(string(body), "q=page=1") {
			t.Fatalf("query 应透传，body=%s", body)
		}
	}

	// Case 3: 不存在路由 → 404
	{
		resp, err := http.Get(gwSrv.URL + "/apps/app_nope/")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("不存在路由应 404，得到 %d", resp.StatusCode)
		}
	}
}

// auth_required=false 不验 JWT，直接反代（用于应用对外开放公开服务）。
func TestGateway_ReverseProxyNoAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	addr, seenHdr, hit := startUpstream(t)
	host, port, _ := netSplitHostPort(addr)
	portI := 0
	for _, ch := range port {
		portI = portI*10 + int(ch-'0')
	}
	route := &Route{
		AppCode: "app_pub", Env: "prod",
		ProjectSpaceID: "ps_1", UpstreamHost: host, UpstreamPort: portI,
		Status: StatusActive, AuthRequired: false,
	}
	g := NewGateway(newFakeStore(route), nil, nil, nil) // auth=nil
	r := gin.New()
	r.Any("/apps/*any", g.ReverseProxy)
	gwSrv := httptest.NewServer(r)
	t.Cleanup(gwSrv.Close)

	resp, err := http.Get(gwSrv.URL + "/apps/app_pub/health")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("auth_required=false 应直接 200，得到 %d body=%s", resp.StatusCode, body)
	}
	if *hit != 1 {
		t.Fatalf("应转发 1 次，hit=%d", *hit)
	}
	// 未鉴权不注入 X-User；X-Project-Space-Id 仍注入
	if seenHdr.Get("X-User") != "" {
		t.Fatalf("未鉴权不应注入 X-User，得到 %q", seenHdr.Get("X-User"))
	}
	if seenHdr.Get("X-Project-Space-Id") != "ps_1" {
		t.Fatalf("仍应注入 X-Project-Space-Id，得到 %q", seenHdr.Get("X-Project-Space-Id"))
	}
}

// env=test 走 ~test 后缀；env 默认 prod。
func TestGateway_EnvSuffix(t *testing.T) {
	gin.SetMode(gin.TestMode)
	addr, _, _ := startUpstream(t)
	host, port, _ := netSplitHostPort(addr)
	portI := 0
	for _, ch := range port {
		portI = portI*10 + int(ch-'0')
	}
	// 只挂 test 路由；prod 路由不存在 → 404 验证默认 env
	route := &Route{
		AppCode: "app_e", Env: "test",
		ProjectSpaceID: "ps_1", UpstreamHost: host, UpstreamPort: portI,
		Status: StatusActive, AuthRequired: false,
	}
	g := NewGateway(newFakeStore(route), nil, nil, nil)
	r := gin.New()
	r.Any("/apps/*any", g.ReverseProxy)
	gwSrv := httptest.NewServer(r)
	t.Cleanup(gwSrv.Close)

	// ~test 后缀 → 命中
	resp, err := http.Get(gwSrv.URL + "/apps/app_e~test/foo")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("~test 应命中 test 路由，得到 %d body=%s", resp.StatusCode, body)
	}
	// 默认 env=prod → test-only 应用应 404
	resp2, err := http.Get(gwSrv.URL + "/apps/app_e/foo")
	if err != nil {
		t.Fatalf("get2: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("无 prod 路由应 404，得到 %d", resp2.StatusCode)
	}
}

// captureAccessLogger 收集 recordAccess 异步写的日志，供断言。
type captureAccessLogger struct {
	logs []AccessLog
}

func (c *captureAccessLogger) LogAccess(_ context.Context, al *AccessLog) error {
	c.logs = append(c.logs, *al)
	return nil
}

// access_log 记录验证：鉴权反代 → 记一条 status=200 + caller=alice；
// 不存在的路由（404 在 recordAccess 之前 short-circuit）不记。
func TestGateway_RecordsAccessLog(t *testing.T) {
	gin.SetMode(gin.TestMode)
	addr, _, _ := startUpstream(t)
	host, port, _ := netSplitHostPort(addr)
	portI := 0
	for _, ch := range port {
		portI = portI*10 + int(ch-'0')
	}
	route := &Route{
		AppCode: "app_demo", Env: "prod", AppID: "app_1",
		ProjectSpaceID: "ps_1", UpstreamHost: host, UpstreamPort: portI,
		Status: StatusActive, AuthRequired: true,
	}
	cap := &captureAccessLogger{}
	g := NewGateway(newFakeStore(route), &fakeAuth{okUser: "alice"}, nil, cap)
	r := gin.New()
	r.Any("/apps/*any", g.ReverseProxy)
	gwSrv := httptest.NewServer(r)
	t.Cleanup(gwSrv.Close)

	req, _ := http.NewRequest("GET", gwSrv.URL+"/apps/app_demo/api/list", nil)
	req.Header.Set("Authorization", "Bearer tok_ok")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("应 200，得到 %d", resp.StatusCode)
	}

	// recordAccess 异步写，等一下
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && len(cap.logs) == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	if len(cap.logs) != 1 {
		t.Fatalf("应记 1 条 access_log，得到 %d", len(cap.logs))
	}
	al := cap.logs[0]
	if al.Status != 200 || al.Caller != "alice" || al.AppID != "app_1" ||
		al.Method != "GET" || al.Path != "/apps/app_demo/api/list" || al.LatencyMs < 0 {
		t.Fatalf("access_log 字段不符: %+v", al)
	}
}

// upstream 不可达 → ErrorHandler 触发 → 记一条 status=502。
func TestGateway_RecordsAccessLogOn502(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// 路由指到一个不存在的端口 → upstream 连接失败 → ErrorHandler → 502
	route := &Route{
		AppCode: "app_dead", Env: "prod", AppID: "app_dead",
		ProjectSpaceID: "ps_1", UpstreamHost: "127.0.0.1", UpstreamPort: 1, // 1 号端口几乎必然不开
		Status: StatusActive, AuthRequired: false,
	}
	cap := &captureAccessLogger{}
	g := NewGateway(newFakeStore(route), nil, nil, cap)
	r := gin.New()
	r.Any("/apps/*any", g.ReverseProxy)
	gwSrv := httptest.NewServer(r)
	t.Cleanup(gwSrv.Close)

	resp, err := http.Get(gwSrv.URL + "/apps/app_dead/foo")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("应 502，得到 %d", resp.StatusCode)
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && len(cap.logs) == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	if len(cap.logs) != 1 {
		t.Fatalf("502 应记 1 条 access_log，得到 %d", len(cap.logs))
	}
	if cap.logs[0].Status != 502 || cap.logs[0].Caller != "anonymous" {
		t.Fatalf("502 access_log 字段不符: %+v", cap.logs[0])
	}
}

// resolveCaller 推断逻辑：user 优先 / 次选 X-Api-Key / 兜底 anonymous。
func TestResolveCaller(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		user, apiKey, want string
	}{
		{"alice", "", "alice"},
		{"", "sk-secret-key-1234", "apikey:sk-secre"}, // 取前 8 字符
		{"", "short", "apikey:short"},                 // 短 key 原样
		{"", "", "anonymous"},
	}
	for _, c := range cases {
		gc, _ := gin.CreateTestContext(nil)
		gc.Request, _ = http.NewRequest("GET", "/", nil)
		if c.apiKey != "" {
			gc.Request.Header.Set("X-Api-Key", c.apiKey)
		}
		if got := resolveCaller(gc, c.user); got != c.want {
			t.Errorf("resolveCaller(user=%q,key=%q) = %q, want %q", c.user, c.apiKey, got, c.want)
		}
	}
}

// TestGateway_ExternalURL external 应用反代：route.external_url 非空 → gateway 直接反代到此 URL。
// 验证两点：
//  1. 路径前缀剥离 /apps/<code>/ 不变（rest 透传到 external_url）
//  2. external_url 自带路径（/prefix）应拼到 rest 前 → /prefix/<rest>
func TestGateway_ExternalURL(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// 假"外部应用"：回显收到的 path + query
	var seenPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path + "?" + r.URL.RawQuery
		w.Header().Set("X-Upstream-Path", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "external hit; path="+r.URL.Path)
	}))
	t.Cleanup(srv.Close)

	// Case 1: external_url 无路径（http://host:port），rest 透传为 /<rest>
	t.Run("no_prefix", func(t *testing.T) {
		route := &Route{
			AppCode: "app_ext", Env: "prod",
			ProjectSpaceID: "ps_1",
			ExternalURL:    srv.URL, // 形如 http://127.0.0.1:port（无路径）
			Status:         StatusActive, AuthRequired: false,
		}
		g := NewGateway(newFakeStore(route), nil, nil, nil)
		r := gin.New()
		r.Any("/apps/*any", g.ReverseProxy)
		gwSrv := httptest.NewServer(r)
		t.Cleanup(gwSrv.Close)

		resp, err := http.Get(gwSrv.URL + "/apps/app_ext/api/list?page=1")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("应 200，得到 %d body=%s", resp.StatusCode, body)
		}
		// upstream 看到的 path = /api/list（rest 透传）
		if seenPath != "/api/list?page=1" {
			t.Fatalf("upstream path 应 /api/list?page=1，得到 %q", seenPath)
		}
	})

	// Case 2: external_url 自带路径前缀（http://host:port/prefix）→ 反代到 /prefix/<rest>
	t.Run("with_prefix", func(t *testing.T) {
		route := &Route{
			AppCode: "app_ext2", Env: "prod",
			ProjectSpaceID: "ps_1",
			ExternalURL:    srv.URL + "/erp/api",
			Status:         StatusActive, AuthRequired: false,
		}
		g := NewGateway(newFakeStore(route), nil, nil, nil)
		r := gin.New()
		r.Any("/apps/*any", g.ReverseProxy)
		gwSrv := httptest.NewServer(r)
		t.Cleanup(gwSrv.Close)

		resp, err := http.Get(gwSrv.URL + "/apps/app_ext2/users?id=42")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("应 200，得到 %d body=%s", resp.StatusCode, body)
		}
		// external_url 的 /erp/api 前缀 + rest(users) → /erp/api/users?id=42
		if seenPath != "/erp/api/users?id=42" {
			t.Fatalf("应拼前缀 /erp/api/users?id=42，得到 %q", seenPath)
		}
	})
}

// TestGateway_ExternalURL_BadRoute external_url 非法 → 502（不 panic）。
func TestGateway_ExternalURL_BadRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	route := &Route{
		AppCode: "app_bad", Env: "prod",
		ProjectSpaceID: "ps_1",
		ExternalURL:    "://not-a-url", // url.Parse 解析后 Host 空
		Status:         StatusActive, AuthRequired: false,
	}
	g := NewGateway(newFakeStore(route), nil, nil, nil)
	r := gin.New()
	r.Any("/apps/*any", g.ReverseProxy)
	gwSrv := httptest.NewServer(r)
	t.Cleanup(gwSrv.Close)

	resp, err := http.Get(gwSrv.URL + "/apps/app_bad/foo")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("非法 external_url 应 502，得到 %d", resp.StatusCode)
	}
}
