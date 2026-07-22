package change

import (
	"context"
	"errors"
	"testing"

	"zhiyuan-anp/platform/backend/internal/testutil"
)

// newTestStore 连 anp_test PG（testutil 跑迁移建平台全表）+ 清本模块涉及的 3 表隔离。
// 涉及 change_request(主) + JOIN 依赖的 appdeploy_application/requirement（app_name 双路径）。
// 替代 sqlite :memory:（sqlite 漏 PG 类型 bug，见 memory sqlite-test-pg-type-trap）。
func newTestStore(t *testing.T) *Store {
	t.Helper()
	db := testutil.TestDB(t)
	testutil.Truncate(t, db, "change_request", "requirement", "appdeploy_application")
	return NewStore(db)
}

func mk(ps, src string) *ChangeRequest {
	return &ChangeRequest{ProjectSpaceID: ps, Kind: "code", SourceID: src, Model: "glm-5.1", Output: "diff..."}
}

// TestCreateAndGet 登记→读回，初始 status=pending。
func TestCreateAndGet(t *testing.T) {
	s := newTestStore(t)
	c := mk("ps_1", "app_1")
	if err := s.Create(context.Background(), c); err != nil {
		t.Fatalf("create: %v", err)
	}
	if c.ID == "" {
		t.Fatal("create 后应填充 ID")
	}
	got, err := s.Get(context.Background(), c.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != "pending" {
		t.Fatalf("新建变更 status 应为 pending，得到 %s", got.Status)
	}
	if got.SourceID != "app_1" {
		t.Fatalf("SourceID 不匹配：%s", got.SourceID)
	}
}

// TestDecide_ApprovedAndHasApproved 审批通过后 HasApproved=true。
func TestDecide_ApprovedAndHasApproved(t *testing.T) {
	s := newTestStore(t)
	c := mk("ps_1", "app_1")
	_ = s.Create(context.Background(), c)

	if has, _ := s.HasApproved(context.Background(), "app_1"); has {
		t.Fatal("未审批前 HasApproved 应为 false")
	}
	if err := s.Decide(context.Background(), c.ID, "approved", "admin"); err != nil {
		t.Fatalf("decide approved: %v", err)
	}
	if has, _ := s.HasApproved(context.Background(), "app_1"); !has {
		t.Fatal("审批后 HasApproved 应为 true")
	}
}

// TestDecide_NotPendingReturnsError 异常边界：非 pending 状态再次审批须返回 errNotPending。
// 防止重复审批/已决变更被覆盖。
func TestDecide_NotPendingReturnsError(t *testing.T) {
	s := newTestStore(t)
	c := mk("ps_1", "app_1")
	_ = s.Create(context.Background(), c)

	if err := s.Decide(context.Background(), c.ID, "approved", "admin"); err != nil {
		t.Fatalf("首次审批应成功: %v", err)
	}
	err := s.Decide(context.Background(), c.ID, "rejected", "admin2")
	if !errors.Is(err, errNotPending) {
		t.Fatalf("非 pending 状态审批应返回 errNotPending，得到 %v", err)
	}
}

// TestMarkReleased approved→released，且不再算 approved（从待上线消失）。
func TestMarkReleased(t *testing.T) {
	s := newTestStore(t)
	c := mk("ps_1", "app_1")
	_ = s.Create(context.Background(), c)
	_ = s.Decide(context.Background(), c.ID, "approved", "admin")

	if err := s.MarkReleased(context.Background(), "app_1"); err != nil {
		t.Fatalf("mark released: %v", err)
	}
	got, _ := s.Get(context.Background(), c.ID)
	if got.Status != "released" {
		t.Fatalf("MarkReleased 后 status 应为 released，得到 %s", got.Status)
	}
	if has, _ := s.HasApproved(context.Background(), "app_1"); has {
		t.Fatal("released 后 HasApproved 应为 false")
	}
}

// TestList_AppName List/Get 返回的 app_name 经双路径 JOIN:
//   - source_id=app_id → 直接 JOIN appdeploy_application
//   - source_id=requirement_id → 经 requirement.application_id JOIN
//
// 各中心据此显示应用名而非 chg_xxx 随机 ID。
func TestList_AppName(t *testing.T) {
	s := newTestStore(t)
	s.db.MustExec(`INSERT INTO appdeploy_application (id, project_space_id, name) VALUES ('app_1', 'ps_1', 'hello-go')`)
	s.db.MustExec(`INSERT INTO appdeploy_application (id, project_space_id, name) VALUES ('app_2', 'ps_1', 'chat-app')`)
	s.db.MustExec(`INSERT INTO requirement (id, project_space_id, application_id, title) VALUES ('req_2', 'ps_1', 'app_2', 't')`)

	c1 := mk("ps_1", "app_1")
	_ = s.Create(context.Background(), c1)
	c2 := mk("ps_1", "req_2")
	_ = s.Create(context.Background(), c2)

	list, err := s.List(context.Background(), "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	got := map[string]string{}
	for _, c := range list {
		got[c.SourceID] = c.AppName
	}
	if got["app_1"] != "hello-go" {
		t.Fatalf("source_id=app_id 应 JOIN 出 hello-go,得到 %q", got["app_1"])
	}
	if got["req_2"] != "chat-app" {
		t.Fatalf("source_id=requirement_id 经 application_id 应 JOIN 出 chat-app,得到 %q", got["req_2"])
	}
	// Get 同样带 app_name
	g, _ := s.Get(context.Background(), c1.ID)
	if g.AppName != "hello-go" {
		t.Fatalf("Get 应带 app_name=hello-go,得到 %q", g.AppName)
	}
}

// TestHasApproved_ViaRequirement source_id=requirement_id(AI 编码派生)时,经 requirement.application_id 识别归属应用。
// 闸门三方法必须双路径,否则 req 派生的变更被漏判。
func TestHasApproved_ViaRequirement(t *testing.T) {
	s := newTestStore(t)
	s.db.MustExec(`INSERT INTO appdeploy_application (id, project_space_id, name) VALUES ('app_1', 'ps_1', 'hello')`)
	s.db.MustExec(`INSERT INTO requirement (id, project_space_id, application_id, title) VALUES ('req_1', 'ps_1', 'app_1', 't')`)
	c := mk("ps_1", "req_1") // source_id=requirement_id
	_ = s.Create(context.Background(), c)
	_ = s.Decide(context.Background(), c.ID, "approved", "admin")
	if has, _ := s.HasApproved(context.Background(), "app_1"); !has {
		t.Fatal("source_id=req_id 经 application_id 应识别为 app_1 的 approved 变更")
	}
	if hasAny, _ := s.HasAny(context.Background(), "app_1"); !hasAny {
		t.Fatal("HasAny 双路径应识别 req_id 派生的变更")
	}
}

// TestMarkReleased_ViaRequirement source_id=req_id 的 approved 变更,MarkReleased(app_id) 后应 released。
// dogfooding 暴露:原仅 source_id=app_id,漏 req_id 派生 → 上线后变更不标 released(用户反馈"还显示")。
func TestMarkReleased_ViaRequirement(t *testing.T) {
	s := newTestStore(t)
	s.db.MustExec(`INSERT INTO appdeploy_application (id, project_space_id, name) VALUES ('app_1', 'ps_1', 'hello')`)
	s.db.MustExec(`INSERT INTO requirement (id, project_space_id, application_id, title) VALUES ('req_1', 'ps_1', 'app_1', 't')`)
	c := mk("ps_1", "req_1")
	_ = s.Create(context.Background(), c)
	_ = s.Decide(context.Background(), c.ID, "approved", "admin")
	if err := s.MarkReleased(context.Background(), "app_1"); err != nil {
		t.Fatalf("mark released: %v", err)
	}
	got, _ := s.Get(context.Background(), c.ID)
	if got.Status != "released" {
		t.Fatalf("source_id=req_id 的变更 MarkReleased(app_id) 后应 released,得到 %s", got.Status)
	}
}
