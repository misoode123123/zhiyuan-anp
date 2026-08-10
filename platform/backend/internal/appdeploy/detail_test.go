package appdeploy

import (
	"context"
	"testing"
	"time"

	"zhiyuan-anp/platform/backend/internal/testutil"
)

// TestDetail_EmptySlicesNotNil 无需求/变更/发布/提交/实例的新应用,Detail 各切片须非 nil。
// Go nil 切片序列化为 JSON null,前端 detail.requirements.length 会崩(应用详情打不开)。
// 本测试是该根因修复的回归保护。
func TestDetail_EmptySlicesNotNil(t *testing.T) {
	s := newTestStore(t)
	ps := "ps_detail_nil"
	a := &Application{ProjectSpaceID: ps, Name: "empty-app", AppKind: AppKindWeb, InternalPort: 3000}
	if err := s.Create(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	d, err := s.Detail(context.Background(), ps, a.ID)
	if err != nil || d == nil {
		t.Fatalf("Detail err=%v d=%v", err, d)
	}
	// 直接比较类型化切片与 nil(不经 interface{},避免 nil-interface 陷阱)。
	if d.Requirements == nil {
		t.Fatal("Requirements 为 nil → JSON null → 前端 .length 崩")
	}
	if d.Changes == nil {
		t.Fatal("Changes 为 nil → JSON null → 前端 .length 崩")
	}
	if d.Releases == nil {
		t.Fatal("Releases 为 nil → JSON null → 前端 .length 崩")
	}
	if d.Commits == nil {
		t.Fatal("Commits 为 nil → JSON null → 前端 .map 崩")
	}
	if d.Instances == nil {
		t.Fatal("Instances 为 nil → JSON null → 前端 .map 崩")
	}
}

// seedDetailStore 连 anp_test PG + 清 9 表隔离（appdeploy 三表 + Detail 聚合的 6 源/派生表）。
// 比 newTestStore 多清 codews_session/appdeploy_route/code_task/change_request/release_record/requirement，
// 防 Detail 聚合维度被其它测试残留污染。
func seedDetailStore(t *testing.T) *Store {
	t.Helper()
	db := testutil.TestDB(t)
	testutil.Truncate(t, db,
		"appdeploy_env", "appdeploy_instance", "appdeploy_application",
		"codews_session", "appdeploy_route", "code_task",
		"change_request", "release_record", "requirement")
	return NewStore(db)
}

// TestDetail_AggregatesAllDimensions seed 应用 + 编码会话 + 路由 + 变更+异步任务，
// 断言 Detail() 把四源聚合进 AppFullView，且 deploy needs（无 manifest 时）为 nil 不崩。
func TestDetail_AggregatesAllDimensions(t *testing.T) {
	s := seedDetailStore(t)
	ctx := context.Background()
	a := mkApp("ps_1", "panorama")
	if err := s.Create(ctx, a); err != nil {
		t.Fatalf("create app: %v", err)
	}
	// 编码会话（codews_session.app_id 直关联）
	mustExec(t, ctx, s,
		`INSERT INTO codews_session (id, project_space_id, app_id, tool, repo_dir, prompt_count)
		 VALUES ($1,'ps_1',$2,'opencode','/data/repos/panorama',3)`, "cws_1", a.ID)
	// 路由（appdeploy_route.app_id FK → app）
	mustExec(t, ctx, s,
		`INSERT INTO appdeploy_route (id, app_id, project_space_id, app_code, env, upstream_host, upstream_port, status, auth_required)
		 VALUES ($1,$2,'ps_1','panorama','test','10.10.0.28',9100,'active',false)`, "rt_1", a.ID)
	// 变更（change_request.source_id=appID 直登）+ 异步任务（code_task.change_id → change）
	mustExec(t, ctx, s,
		`INSERT INTO change_request (id, source_id, status, kind, created_at)
		 VALUES ($1,$2,'pending','code',$3)`, "ch_1", a.ID, time.Now())
	mustExec(t, ctx, s,
		`INSERT INTO code_task (id, project_space_id, kind, source_id, repo_dir, prompt, model, status, change_id)
		 VALUES ($1,'ps_1','dispatch',$2,'/data/repos/panorama','do','m','completed',$3)`,
		"ct_1", a.ID, "ch_1")

	d, err := s.Detail(ctx, "ps_1", a.ID)
	if err != nil || d == nil {
		t.Fatalf("Detail err=%v d=%v", err, d)
	}
	if len(d.Sessions) != 1 || d.Sessions[0].ID != "cws_1" {
		t.Fatalf("Sessions 聚合错 got=%+v", d.Sessions)
	}
	if len(d.Routes) != 1 || d.Routes[0].Env != "test" {
		t.Fatalf("Routes 聚合错 got=%+v", d.Routes)
	}
	if len(d.Tasks) != 1 || d.Tasks[0].ID != "ct_1" {
		t.Fatalf("Tasks 聚合错 got=%+v", d.Tasks)
	}
	// Deps 在 Store.Detail 不填（handler 层经 mwReconciler 填）；此处应为空切片（nil 归一化）
	if d.Deps == nil {
		t.Fatal("Deps 应 nil 归一化为空切片非 nil")
	}
	// 无 .anp/deploy.yaml（RepoDir=/data/repos/panorama 不存在）→ DeployNeeds nil 不崩
	if d.DeployNeeds != nil {
		t.Fatalf("无 manifest 时 DeployNeeds 应 nil got=%+v", d.DeployNeeds)
	}
}

// mustExec 测试辅助：ExecContext 失败即 fatal。
func mustExec(t *testing.T, ctx context.Context, s *Store, query string, args ...any) {
	t.Helper()
	if _, err := s.db.ExecContext(ctx, query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}
