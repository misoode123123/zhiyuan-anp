package attendance

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

// newTestStore 建内存 SQLite + 仅 attendance_record 表（自包含，仿 change/store_test.go 模式）。
// 类型映射：PG TIMESTAMP→SQLite DATETIME，其余 TEXT 保持。
func newTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := sqlx.Connect("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.MustExec(`CREATE TABLE attendance_record (
  id               TEXT PRIMARY KEY,
  project_space_id TEXT NOT NULL,
  user_id          TEXT NOT NULL,
  status           TEXT NOT NULL,
  start_time       DATETIME NOT NULL,
  end_time         DATETIME NOT NULL,
  reason           TEXT,
  supervisor_id    TEXT NOT NULL,
  approval_status  TEXT NOT NULL DEFAULT 'pending',
  approver         TEXT,
  approved_at      DATETIME,
  created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`)
	return NewStore(db)
}

// mk 构造一条待审批考勤记录，ID 用 uuid 保证唯一。
func mk(ps, user, supervisor string) *AttendanceRecord {
	start := time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC)
	return &AttendanceRecord{
		ID:             "att_" + uuid.NewString()[:20],
		ProjectSpaceID: ps,
		UserID:         user,
		Status:         StatusRest,
		StartTime:      start,
		EndTime:        start.Add(8 * time.Hour),
		Reason:         "午休",
		SupervisorID:   supervisor,
		ApprovalStatus: ApprovalPending,
	}
}

// TestCreateAndGet 登记后读回，默认 approval_status=pending。
func TestCreateAndGet(t *testing.T) {
	s := newTestStore(t)
	rec := mk("ps_1", "u_alice", "sup_bob")
	if err := s.Create(context.Background(), rec); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := s.Get(context.Background(), rec.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.UserID != "u_alice" {
		t.Fatalf("UserID 不匹配：%s", got.UserID)
	}
	if got.ApprovalStatus != ApprovalPending {
		t.Fatalf("新建考勤 approval_status 应为 pending，得到 %s", got.ApprovalStatus)
	}
	if got.Status != StatusRest {
		t.Fatalf("Status 不匹配：%s", got.Status)
	}
	if got.Reason != "午休" {
		t.Fatalf("Reason 不匹配：%s", got.Reason)
	}
	if got.SupervisorID != "sup_bob" {
		t.Fatalf("SupervisorID 不匹配：%s", got.SupervisorID)
	}
	// created_at/updated_at 由 DB 默认值生成，读回应非零。
	if got.CreatedAt.IsZero() {
		t.Fatal("created_at 未被默认值填充")
	}
}

// TestGet_NotFound 查询不存在的 ID 应返回 sql.ErrNoRows（包裹亦可）。
func TestGet_NotFound(t *testing.T) {
	s := newTestStore(t)
	_, err := s.Get(context.Background(), "att_does_not_exist")
	if err == nil {
		t.Fatal("未找到时应返回 error")
	}
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("应返回 ErrNoRows，得到 %v", err)
	}
}

// TestListByProjectSpace 按项目空间过滤（不同 PS 的记录不串味）。
// 注：SQLite CURRENT_TIMESTAMP 仅秒级精度，跨秒可能不足，故只校验集合而非顺序。
func TestListByProjectSpace(t *testing.T) {
	s := newTestStore(t)
	a := mk("ps_1", "u_a", "sup_bob")
	b := mk("ps_1", "u_b", "sup_bob")
	c := mk("ps_2", "u_c", "sup_bob") // 不同项目空间
	for _, r := range []*AttendanceRecord{a, b, c} {
		if err := s.Create(context.Background(), r); err != nil {
			t.Fatalf("create: %v", err)
		}
	}
	got, err := s.ListByProjectSpace(context.Background(), "ps_1")
	if err != nil {
		t.Fatalf("list by ps: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ps_1 应有 2 条，得到 %d", len(got))
	}
	// 返回集合应恰好等于 {a, b}，不含 c
	gotIDs := map[string]bool{got[0].ID: true, got[1].ID: true}
	if !gotIDs[a.ID] || !gotIDs[b.ID] {
		t.Fatalf("返回集合应为 {a, b}，得到 %v", gotIDs)
	}
}

// TestListByProjectSpace_Order 同一秒内也用 created_at DESC，跨秒场景断言顺序。
// 通过显式设置 created_at（绕过 SQLite 默认值的秒级精度）来锁定排序行为。
func TestListByProjectSpace_Order(t *testing.T) {
	s := newTestStore(t)
	// 直接用原生 SQL 注入不同 created_at，避开 Create 不支持设置 created_at 的限制。
	mustExec(t, s.db,
		`INSERT INTO attendance_record (id, project_space_id, user_id, status, start_time, end_time, supervisor_id, approval_status, created_at, updated_at)
		 VALUES ('att_old', 'ps_1', 'u_a', 'rest', '2026-07-20 09:00:00', '2026-07-20 17:00:00', 'sup_bob', 'pending', '2026-07-20 10:00:00', '2026-07-20 10:00:00')`)
	mustExec(t, s.db,
		`INSERT INTO attendance_record (id, project_space_id, user_id, status, start_time, end_time, supervisor_id, approval_status, created_at, updated_at)
		 VALUES ('att_new', 'ps_1', 'u_b', 'rest', '2026-07-20 09:00:00', '2026-07-20 17:00:00', 'sup_bob', 'pending', '2026-07-20 11:00:00', '2026-07-20 11:00:00')`)
	got, err := s.ListByProjectSpace(context.Background(), "ps_1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("应有 2 条，得到 %d", len(got))
	}
	// DESC：新的（11:00）应排前
	if got[0].ID != "att_new" {
		t.Fatalf("DESC 排序错误：首条应为 att_new，得到 %s", got[0].ID)
	}
}

// mustExec 测试辅助：失败即 fatal。
func mustExec(t *testing.T, db *sqlx.DB, q string) {
	t.Helper()
	if _, err := db.Exec(q); err != nil {
		t.Fatalf("exec %q: %v", q, err)
	}
}

// TestListByProjectSpace_Empty 项目空间下无记录返回空切片（非 nil 也算 len==0）。
func TestListByProjectSpace_Empty(t *testing.T) {
	s := newTestStore(t)
	got, err := s.ListByProjectSpace(context.Background(), "ps_empty")
	if err != nil {
		t.Fatalf("list empty: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("空项目空间应返回 0 条，得到 %d", len(got))
	}
}

// TestListByUser 「我的考勤」按 user_id 过滤。
func TestListByUser(t *testing.T) {
	s := newTestStore(t)
	_ = s.Create(context.Background(), mk("ps_1", "u_alice", "sup_bob"))
	_ = s.Create(context.Background(), mk("ps_1", "u_alice", "sup_bob"))
	_ = s.Create(context.Background(), mk("ps_1", "u_bob", "sup_bob"))
	got, err := s.ListByUser(context.Background(), "ps_1", "u_alice")
	if err != nil {
		t.Fatalf("list by user: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("u_alice 应有 2 条，得到 %d", len(got))
	}
	for _, r := range got {
		if r.UserID != "u_alice" {
			t.Fatalf("ListByUser 返回了非 alice 记录：%s", r.UserID)
		}
	}
}

// TestListBySupervisor 「审批收件箱」：默认全部状态、按 supervisor_id 过滤。
func TestListBySupervisor(t *testing.T) {
	s := newTestStore(t)
	a := mk("ps_1", "u_alice", "sup_bob")
	b := mk("ps_1", "u_carol", "sup_bob")
	c := mk("ps_1", "u_dave", "sup_eve") // 不同上级
	if err := s.Create(context.Background(), a); err != nil {
		t.Fatalf("create a: %v", err)
	}
	if err := s.Create(context.Background(), b); err != nil {
		t.Fatalf("create b: %v", err)
	}
	if err := s.Create(context.Background(), c); err != nil {
		t.Fatalf("create c: %v", err)
	}
	// 把 a 审批通过，便于下一条用例断言按 approval_status 过滤
	if err := s.UpdateApproval(context.Background(), a.ID, ApprovalApproved, "sup_bob"); err != nil {
		t.Fatalf("update approval: %v", err)
	}
	// 不传 approvalStatus：应返回 sup_bob 名下全部 2 条（a+b）
	all, err := s.ListBySupervisor(context.Background(), "sup_bob", "")
	if err != nil {
		t.Fatalf("list all by supervisor: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("sup_bob 应有 2 条（含已审批），得到 %d", len(all))
	}
	// 仅 pending：只剩 b（a 已 approved）
	pending, err := s.ListBySupervisor(context.Background(), "sup_bob", ApprovalPending)
	if err != nil {
		t.Fatalf("list pending by supervisor: %v", err)
	}
	if len(pending) != 1 || pending[0].ID != b.ID {
		t.Fatalf("pending 应仅含 b，得到 %+v", pending)
	}
	// 仅 approved：只剩 a
	approved, err := s.ListBySupervisor(context.Background(), "sup_bob", ApprovalApproved)
	if err != nil {
		t.Fatalf("list approved by supervisor: %v", err)
	}
	if len(approved) != 1 || approved[0].ID != a.ID {
		t.Fatalf("approved 应仅含 a，得到 %+v", approved)
	}
}

// TestUpdateApproval 审批更新写 approver/approved_at，并使后续 ListBySupervisor(pending) 不再返回。
func TestUpdateApproval(t *testing.T) {
	s := newTestStore(t)
	rec := mk("ps_1", "u_alice", "sup_bob")
	if err := s.Create(context.Background(), rec); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.UpdateApproval(context.Background(), rec.ID, ApprovalApproved, "sup_bob"); err != nil {
		t.Fatalf("update approval: %v", err)
	}
	got, err := s.Get(context.Background(), rec.ID)
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	if got.ApprovalStatus != ApprovalApproved {
		t.Fatalf("approval_status 应为 approved，得到 %s", got.ApprovalStatus)
	}
	if got.Approver != "sup_bob" {
		t.Fatalf("approver 应为 sup_bob，得到 %s", got.Approver)
	}
	if got.ApprovedAt == nil || got.ApprovedAt.IsZero() {
		t.Fatal("approved_at 应被填充")
	}
}

// TestUpdateApproval_NotFound 更新不存在的 ID 不报错也不写入（当前实现未校验 RowsAffected）。
// 锁定当前行为；若日后改成报错，本用例需同步调整。
func TestUpdateApproval_NotFound(t *testing.T) {
	s := newTestStore(t)
	if err := s.UpdateApproval(context.Background(), "att_nope", ApprovalApproved, "sup_bob"); err != nil {
		t.Fatalf("更新不存在的记录当前实现应静默返回 nil，得到 %v", err)
	}
}
