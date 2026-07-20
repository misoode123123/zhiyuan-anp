package qa

import (
	"context"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

// newTestStore 建内存 SQLite + 仅 test_case 表（自包含，仿 change/store_test.go 模式）。
// 类型映射：PostgreSQL TIMESTAMP→DATETIME，其余 TEXT/INTEGER 同名兼容。
func newTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := sqlx.Connect("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.MustExec(`CREATE TABLE test_case (
  id               TEXT PRIMARY KEY,
  project_space_id TEXT NOT NULL,
  requirement_id   TEXT,
  title            TEXT NOT NULL,
  steps            TEXT,
  expected         TEXT,
  status           TEXT NOT NULL DEFAULT 'draft',
  method           TEXT,
  path             TEXT,
  expected_status  INTEGER,
  expected_body    TEXT,
  actual_status    INTEGER,
  actual_body      TEXT,
  run_at           DATETIME,
  created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`)
	return NewStore(db)
}

// mkTC 构造一条最小可用 TestCase，调用方可覆盖字段。
func mkTC(ps, rid, title string) *TestCase {
	return &TestCase{
		ID:             "tc_" + title,
		ProjectSpaceID: ps,
		RequirementID:  rid,
		Title:          title,
		Steps:          `["打开首页"]`,
		Expected:       "返回 200",
		Status:         "draft",
		Method:         "GET",
		Path:           "/",
		ExpectedStatus: 200,
		ExpectedBody:   "ok",
	}
}

// TestCreateAndGet 新建→读回，字段一一对应；默认 status=draft 由 Create 显式传入。
func TestCreateAndGet(t *testing.T) {
	s := newTestStore(t)
	tc := mkTC("ps_1", "req_1", "首页返回200")
	if err := s.Create(context.Background(), tc); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := s.Get(context.Background(), tc.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != tc.ID {
		t.Fatalf("ID 不匹配：got=%s want=%s", got.ID, got.ID)
	}
	if got.Title != "首页返回200" {
		t.Fatalf("Title 不匹配：%s", got.Title)
	}
	if got.Status != "draft" {
		t.Fatalf("新建 status 应为 draft，得到 %s", got.Status)
	}
	if got.ProjectSpaceID != "ps_1" || got.RequirementID != "req_1" {
		t.Fatalf("外键映射错误：ps=%s rid=%s", got.ProjectSpaceID, got.RequirementID)
	}
	if got.ExpectedStatus != 200 {
		t.Fatalf("ExpectedStatus 应为 200，得到 %d", got.ExpectedStatus)
	}
	if got.CreatedAt.IsZero() {
		t.Fatal("CreatedAt 应被数据库默认值填充")
	}
}

// TestCreate_NullableFields 边界：requirement_id/method/path/steps/expected 等可空字段传空串，
// 经 COALESCE 读回也应为空串而非 NULL（防 string 扫描 NULL 报错）。
func TestCreate_NullableFields(t *testing.T) {
	s := newTestStore(t)
	tc := &TestCase{
		ID:             "tc_min",
		ProjectSpaceID: "ps_1",
		Title:          "纯人工用例",
		Status:         "draft",
		// RequirementID/Method/Path/Steps/Expected/ExpectedBody 全部留空
	}
	if err := s.Create(context.Background(), tc); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := s.Get(context.Background(), "tc_min")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.RequirementID != "" || got.Method != "" || got.Path != "" {
		t.Fatalf("可空字段应为空串：rid=%q method=%q path=%q", got.RequirementID, got.Method, got.Path)
	}
	if got.ExpectedStatus != 0 {
		t.Fatalf("未设 ExpectedStatus 应读回 0，得到 %d", got.ExpectedStatus)
	}
}

// TestGet_NotFound 未找到：Get 应返回错误（sql.ErrNoRows 上浮）。
func TestGet_NotFound(t *testing.T) {
	s := newTestStore(t)
	_, err := s.Get(context.Background(), "not_exist")
	if err == nil {
		t.Fatal("查不到的 id 应返回 error")
	}
}

// TestListByProjectSpace 项目空间过滤正确。
// 注：SQLite CURRENT_TIMESTAMP 仅秒级精度，排序在秒内不保证，故只验证集合成员。
func TestListByProjectSpace(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	// 两条归属 ps_1，一条归属 ps_2，确保过滤生效。
	_ = s.Create(ctx, mkTC("ps_1", "req_1", "case1"))
	_ = s.Create(ctx, mkTC("ps_1", "req_1", "case2"))
	_ = s.Create(ctx, mkTC("ps_2", "req_1", "case3"))
	list, err := s.ListByProjectSpace(ctx, "ps_1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("ps_1 应返回 2 条，得到 %d", len(list))
	}
	ids := map[string]bool{list[0].ID: true, list[1].ID: true}
	if !ids["tc_case1"] || !ids["tc_case2"] {
		t.Fatalf("ps_1 列表应包含 case1/case2，得到 %v", ids)
	}
}

// TestListByProjectSpace_Empty 边界：不存在的项目空间返回空切片无错。
func TestListByProjectSpace_Empty(t *testing.T) {
	s := newTestStore(t)
	list, err := s.ListByProjectSpace(context.Background(), "ps_empty")
	if err != nil {
		t.Fatalf("空列表不应报错：%v", err)
	}
	if len(list) != 0 {
		t.Fatalf("空项目空间应返回 0 条，得到 %d", len(list))
	}
}

// TestListByRequirement 按需求过滤（批量验收场景）。
func TestListByRequirement(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	_ = s.Create(ctx, mkTC("ps_1", "req_A", "a1"))
	_ = s.Create(ctx, mkTC("ps_1", "req_A", "a2"))
	_ = s.Create(ctx, mkTC("ps_1", "req_B", "b1"))
	list, err := s.ListByRequirement(ctx, "req_A")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("req_A 应有 2 条，得到 %d", len(list))
	}
	for _, tc := range list {
		if tc.RequirementID != "req_A" {
			t.Fatalf("过滤错误，出现非 req_A 的用例：%s", tc.RequirementID)
		}
	}
}

// TestUpdateRun_StatusTransition 状态迁移：draft→passed/failed/manual，
// 同时回写 actual_status/actual_body/run_at。
func TestUpdateRun_StatusTransition(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	tc := mkTC("ps_1", "req_1", "case_x")
	_ = s.Create(ctx, tc)

	now := time.Now().UTC().Truncate(time.Second)
	tc.Status = "passed"
	tc.ActualStatus = 200
	tc.ActualBody = "ok"
	tc.RunAt = &now
	if err := s.UpdateRun(ctx, tc); err != nil {
		t.Fatalf("update run passed: %v", err)
	}
	got, _ := s.Get(ctx, tc.ID)
	if got.Status != "passed" || got.ActualStatus != 200 || got.ActualBody != "ok" {
		t.Fatalf("passed 回写错误：status=%s actual=%d body=%q", got.Status, got.ActualStatus, got.ActualBody)
	}
	if got.RunAt == nil || !got.RunAt.Equal(now) {
		t.Fatalf("RunAt 应等于 %v，得到 %v", now, got.RunAt)
	}

	// 再迁移到 failed。
	tc.Status = "failed"
	tc.ActualStatus = 500
	tc.ActualBody = "boom"
	if err := s.UpdateRun(ctx, tc); err != nil {
		t.Fatalf("update run failed: %v", err)
	}
	got, _ = s.Get(ctx, tc.ID)
	if got.Status != "failed" || got.ActualStatus != 500 {
		t.Fatalf("failed 回写错误：status=%s actual=%d", got.Status, got.ActualStatus)
	}
}

// TestUpdateRun_ClearsRunAt 边界：RunAt 传 nil 时库中字段被置空（重新运行场景）。
func TestUpdateRun_ClearsRunAt(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	tc := mkTC("ps_1", "req_1", "case_y")
	_ = s.Create(ctx, tc)
	now := time.Now().UTC()
	tc.RunAt = &now
	tc.Status = "passed"
	_ = s.UpdateRun(ctx, tc)

	// 重置 RunAt 为 nil。
	tc.RunAt = nil
	if err := s.UpdateRun(ctx, tc); err != nil {
		t.Fatalf("update run with nil RunAt: %v", err)
	}
	got, _ := s.Get(ctx, tc.ID)
	if got.RunAt != nil {
		t.Fatalf("RunAt 应为 nil，得到 %v", *got.RunAt)
	}
}

// TestPassedCountByRequirement 发布门禁：仅 status='passed' 计入，其余状态不计。
func TestPassedCountByRequirement(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	mk := func(id, status string) *TestCase {
		tc := mkTC("ps_1", "req_gate", id)
		tc.ID = id
		tc.Status = status
		return tc
	}
	for _, tc := range []*TestCase{
		mk("tc_p1", "passed"), mk("tc_p2", "passed"),
		mk("tc_f1", "failed"), mk("tc_d1", "draft"), mk("tc_m1", "manual"),
	} {
		// Create 会把 Status 入参直接持久化（store.go 显式 INSERT status）。
		if err := s.Create(ctx, tc); err != nil {
			t.Fatalf("create %s: %v", tc.ID, err)
		}
	}

	n, err := s.PassedCountByRequirement(ctx, "req_gate")
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 2 {
		t.Fatalf("req_gate 应有 2 条 passed，得到 %d", n)
	}
}

// TestPassedCountByRequirement_NoMatch 边界：需求无用例或无 passed 时返回 0。
func TestPassedCountByRequirement_NoMatch(t *testing.T) {
	s := newTestStore(t)
	n, err := s.PassedCountByRequirement(context.Background(), "req_empty")
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("空需求 passed 计数应为 0，得到 %d", n)
	}
}

// TestNewStore_NilDB 边界：NewStore 不做非空校验，调用方负责。
// 主要保障 nil 传入不会 panic（仅构造不访问 db）。
func TestNewStore_NilDB(t *testing.T) {
	if got := NewStore(nil); got == nil {
		t.Fatal("NewStore 不应返回 nil")
	}
}

// TestNowTime_ReturnsNonNil nowTime 返回非空时间指针（用于 RunAt 赋值）。
func TestNowTime_ReturnsNonNil(t *testing.T) {
	got := nowTime()
	if got == nil || got.IsZero() {
		t.Fatal("nowTime 应返回非空且非零时间")
	}
	// 与当前时间差应在 1s 内。
	if d := time.Since(*got); d > time.Second || d < -time.Second {
		t.Fatalf("nowTime 偏差过大：%v", d)
	}
}
