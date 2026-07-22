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
	testutil.Truncate(t, db, "appdeploy_route", "appdeploy_application")
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
