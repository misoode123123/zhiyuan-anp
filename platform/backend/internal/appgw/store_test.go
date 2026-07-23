package appgw

import (
	"context"
	"testing"

	"zhiyuan-anp/platform/backend/internal/testutil"
)

// newTestStore 连 anp_test PG + 清表隔离。前置：appdeploy_route.app_id → appdeploy_application。
func newTestStore(t *testing.T) *Store {
	t.Helper()
	db := testutil.TestDB(t)
	testutil.Truncate(t, db, "appgw_access_log", "appdeploy_route", "appdeploy_application")
	// FK 前置：插入 appdeploy_application 行供 route 引用
	db.MustExec(`INSERT INTO appdeploy_application (id, project_space_id, name, internal_port, status)
		VALUES ('app_1','ps_1','t-app',8080,'registered') ON CONFLICT DO NOTHING`)
	return NewStore(db)
}

func TestStore_UpsertAndGetRoute(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.UpsertRoute(ctx, "app_1", "ps_1", "prod", "10.10.0.28", 9201); err != nil {
		t.Fatalf("UpsertRoute: %v", err)
	}
	r, err := s.GetRoute(ctx, "app_1", "prod")
	if err != nil || r == nil {
		t.Fatalf("GetRoute 失败: %v / %+v", err, r)
	}
	if r.AppCode != "app_1" || r.Env != "prod" || r.UpstreamHost != "10.10.0.28" ||
		r.UpstreamPort != 9201 || r.Status != StatusActive || !r.AuthRequired {
		t.Fatalf("路由字段不符: %+v", r)
	}
}

// UpsertRoute 幂等：同 app_id+env 二次调用应更新而非插入（不冲突），并刷新 upstream/port/status。
func TestStore_UpsertIsIdempotentAndUpdate(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.UpsertRoute(ctx, "app_1", "ps_1", "prod", "10.10.0.28", 9201); err != nil {
		t.Fatalf("首次 UpsertRoute: %v", err)
	}
	if err := s.UpsertRoute(ctx, "app_1", "ps_1", "prod", "10.10.0.29", 9300); err != nil {
		t.Fatalf("二次 UpsertRoute: %v", err)
	}
	r, _ := s.GetRoute(ctx, "app_1", "prod")
	if r == nil {
		t.Fatal("二次 upsert 后应仍能查到")
	}
	if r.UpstreamHost != "10.10.0.29" || r.UpstreamPort != 9300 {
		t.Fatalf("二次 upsert 应更新 upstream: %+v", r)
	}
	// UNIQUE(app_code, env) 应保证只有一条
	var n int
	if err := s.db.Get(&n, `SELECT COUNT(*) FROM appdeploy_route WHERE app_code='app_1' AND env='prod'`); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("UNIQUE(app_code,env) 应只有 1 条，得到 %d", n)
	}
}

// GetRoute 查无路由应返回 nil（调用方据此 404）。
func TestStore_GetRouteNotFound(t *testing.T) {
	s := newTestStore(t)
	r, err := s.GetRoute(context.Background(), "app_nope", "prod")
	if r != nil {
		t.Fatalf("不存在应返回 nil，得到 %+v", r)
	}
	if err == nil {
		t.Fatal("不存在应返回 err（sql.ErrNoRows），得到 nil")
	}
}

func TestStore_DeleteRouteByApp(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	_ = s.UpsertRoute(ctx, "app_1", "ps_1", "test", "10.10.0.28", 9100)
	_ = s.UpsertRoute(ctx, "app_1", "ps_1", "prod", "10.10.0.28", 9200)

	if err := s.DeleteRouteByApp(ctx, "app_1"); err != nil {
		t.Fatalf("DeleteRouteByApp: %v", err)
	}
	for _, env := range []string{"test", "prod"} {
		if r, _ := s.GetRoute(ctx, "app_1", env); r != nil {
			t.Fatalf("删除后 env=%s 应查不到，得到 %+v", env, r)
		}
	}
}

func TestStore_SetRouteStatus(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	_ = s.UpsertRoute(ctx, "app_1", "ps_1", "prod", "10.10.0.28", 9200)

	if err := s.SetRouteStatus(ctx, "app_1", "prod", StatusInactive); err != nil {
		t.Fatalf("SetRouteStatus: %v", err)
	}
	r, _ := s.GetRoute(ctx, "app_1", "prod")
	if r == nil || r.Status != StatusInactive {
		t.Fatalf("状态应置 inactive: %+v", r)
	}
}

// TestStore_UpsertExternalRoute external 应用路由：写入后 external_url 字段落库 + 状态 active。
// host/port 从 URL 解析填一份（展示用），gateway 反代直接走 external_url。
func TestStore_UpsertExternalRoute(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.UpsertExternalRoute(ctx, "app_1", "ps_1", "prod", "http://erp.example.com:8088/api"); err != nil {
		t.Fatalf("UpsertExternalRoute: %v", err)
	}
	r, err := s.GetRoute(ctx, "app_1", "prod")
	if err != nil || r == nil {
		t.Fatalf("GetRoute 失败: %v / %+v", err, r)
	}
	if r.ExternalURL != "http://erp.example.com:8088/api" {
		t.Fatalf("external_url 应回显原值，得到 %q", r.ExternalURL)
	}
	if r.Status != StatusActive {
		t.Fatalf("external 路由应 active，得到 %s", r.Status)
	}
	// host/port 从 URL 解析（展示用）
	if r.UpstreamHost != "erp.example.com" {
		t.Fatalf("upstream_host 应从 URL 解析为 erp.example.com，得到 %q", r.UpstreamHost)
	}
	if r.UpstreamPort != 8088 {
		t.Fatalf("upstream_port 应为 8088，得到 %d", r.UpstreamPort)
	}
}

// TestStore_UpsertExternalRoute_DefaultPort 无显式端口时按 scheme 默认（http→80, https→443）。
func TestStore_UpsertExternalRoute_DefaultPort(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	_ = s.UpsertExternalRoute(ctx, "app_1", "ps_1", "prod", "https://erp.example.com")
	r, _ := s.GetRoute(ctx, "app_1", "prod")
	if r.UpstreamPort != 443 {
		t.Fatalf("https 无端口应默认 443，得到 %d", r.UpstreamPort)
	}

	_ = s.UpsertExternalRoute(ctx, "app_1", "ps_1", "test", "http://erp.example.com")
	r2, _ := s.GetRoute(ctx, "app_1", "test")
	if r2.UpstreamPort != 80 {
		t.Fatalf("http 无端口应默认 80，得到 %d", r2.UpstreamPort)
	}
}

// TestStore_UpsertExternalRoute_EmptyURL 空串应报错（必填校验）。
func TestStore_UpsertExternalRoute_EmptyURL(t *testing.T) {
	s := newTestStore(t)
	if err := s.UpsertExternalRoute(context.Background(), "app_1", "ps_1", "prod", ""); err == nil {
		t.Fatal("空 external_url 应报错")
	}
}

// TestStore_UpsertRouteClearsExternalURL 从 external 切回 managed（UpsertRoute）时，
// external_url 应清空 —— 否则 gateway 还会走 external 反代逻辑。
func TestStore_UpsertRouteClearsExternalURL(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// 先 external
	_ = s.UpsertExternalRoute(ctx, "app_1", "ps_1", "prod", "http://ext.example.com")
	// 再 managed（同一 app+env）
	_ = s.UpsertRoute(ctx, "app_1", "ps_1", "prod", "10.10.0.28", 9200)
	r, _ := s.GetRoute(ctx, "app_1", "prod")
	if r.ExternalURL != "" {
		t.Fatalf("切回 managed 后 external_url 应清空，得到 %q", r.ExternalURL)
	}
	if r.UpstreamHost != "10.10.0.28" || r.UpstreamPort != 9200 {
		t.Fatalf("managed 应生效，得到 %+v", r)
	}
}

// LogAccess 写入 + ListAccessLogs 倒序 + limit 截断。
func TestStore_LogAndListAccessLogs(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// 写 3 条（同 app，不同 status），验证顺序倒序 + 字段落库
	logs := []*AccessLog{
		{ProjectSpaceID: "ps_1", AppID: "app_1", AppCode: "app_1", Env: "prod",
			Caller: "alice", Method: "GET", Path: "/apps/app_1/api/q", Status: 200, LatencyMs: 12, TraceID: "t1"},
		{ProjectSpaceID: "ps_1", AppID: "app_1", AppCode: "app_1", Env: "prod",
			Caller: "bob", Method: "POST", Path: "/apps/app_1/api/create", Status: 500, LatencyMs: 340, TraceID: "t2"},
		{ProjectSpaceID: "ps_1", AppID: "app_1", AppCode: "app_1", Env: "prod",
			Caller: "anonymous", Method: "GET", Path: "/apps/app_1/health", Status: 200, LatencyMs: 3},
	}
	for _, al := range logs {
		if err := s.LogAccess(ctx, al); err != nil {
			t.Fatalf("LogAccess: %v", err)
		}
	}

	// ListAccessLogs 默认 limit=50，应返回 3 条
	got, err := s.ListAccessLogs(ctx, "app_1", 0)
	if err != nil {
		t.Fatalf("ListAccessLogs: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("应返回 3 条，得到 %d", len(got))
	}
	// 倒序：最后写的应在最前
	if got[0].Caller != "anonymous" || got[2].Caller != "alice" {
		t.Fatalf("顺序应为倒序，得到 %+v", got)
	}
	// 字段：第 2 条（POST 500，带 trace_id）
	mid := got[1]
	if mid.Method != "POST" || mid.Status != 500 || mid.LatencyMs != 340 || mid.TraceID != "t2" {
		t.Fatalf("字段不符: %+v", mid)
	}
	// id 自动生成
	if mid.ID == "" {
		t.Fatalf("id 应自动生成: %+v", mid)
	}

	// limit=2 截断
	got2, _ := s.ListAccessLogs(ctx, "app_1", 2)
	if len(got2) != 2 {
		t.Fatalf("limit=2 应返回 2 条，得到 %d", len(got2))
	}

	// 不存在的 app → 空列表
	got3, _ := s.ListAccessLogs(ctx, "app_nope", 0)
	if len(got3) != 0 {
		t.Fatalf("不存在的 app 应返回空，得到 %d", len(got3))
	}
}

// caller 可空（anonymous 也写空串）；trace_id 可空 —— 验证 NOT NULL 列不报错。
func TestStore_LogAccessNullableCaller(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	al := &AccessLog{
		ProjectSpaceID: "ps_1", AppID: "app_1", AppCode: "app_1", Env: "prod",
		Method: "GET", Path: "/apps/app_1/", Status: 200, LatencyMs: 1,
		// Caller / TraceID 留空
	}
	if err := s.LogAccess(ctx, al); err != nil {
		t.Fatalf("LogAccess 空 caller/trace_id 应成功: %v", err)
	}
	got, _ := s.ListAccessLogs(ctx, "app_1", 1)
	if len(got) != 1 || got[0].Caller != "" || got[0].TraceID != "" {
		t.Fatalf("空字段应落库为空串: %+v", got)
	}
}
