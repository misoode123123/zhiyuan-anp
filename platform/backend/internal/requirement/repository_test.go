package requirement

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

// newTestRepo 建内存 SQLite + requirement 表（自包含，仿 change/store_test.go 模式）。
//
// DDL 对齐 internal/db/migrations/pg/000001_init.up.sql 的 requirement 表，
// 类型映射 TIMESTAMP→DATETIME、JSONB/TEXT→TEXT。
// 补齐 model 实际使用的 application_id/priority/fixed_version 列
// （迁移文件中遗漏，详见 TestRepository_CreateAndGet 的注释）。
func newTestRepo(t *testing.T) *Repository {
	t.Helper()
	db, err := sqlx.Connect("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.MustExec(`CREATE TABLE requirement (
  id                  TEXT PRIMARY KEY,
  project_space_id    TEXT NOT NULL,
  application_id      TEXT,
  title               TEXT NOT NULL,
  description         TEXT,
  user_story          TEXT,
  acceptance_criteria TEXT,
  status              TEXT NOT NULL DEFAULT 'draft',
  priority            TEXT,
  fixed_version       TEXT,
  tasks               TEXT,
  assignee            TEXT,
  assigned_at         DATETIME,
  created_at          DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at          DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`)
	return NewRepository(db)
}

// mkReq 构造一条可写库的需求（id 由调用方指定便于断言）。
func mkReq(id, psID string) *Requirement {
	return &Requirement{
		ID:                 id,
		ProjectSpaceID:     psID,
		Title:              "登录页改造",
		Description:        "增加 SSO 登录",
		UserStory:          "作为访客，我希望用 SSO 登录，以便快速进入系统",
		AcceptanceCriteria: `["点击 SSO 跳转","回调后自动登录"]`,
		Status:             "specified",
		Priority:           "P1",
		FixedVersion:       "v1.2",
	}
}

// mustCreateRepo 写入一条需求，失败即 t.Fatal。
func mustCreateRepo(t *testing.T, r *Repository, req *Requirement) {
	t.Helper()
	if err := r.Create(context.Background(), req); err != nil {
		t.Fatalf("create: %v", err)
	}
}

// TestRepository_CreateAndGet 写入→读回，校验所有列往返一致。
// 额外发现（潜在 bug）：迁移 pg/000001_init.up.sql 的 requirement 表 DDL
// 缺 application_id/priority/fixed_version 三列，而 repository.Create/reqCols 均引用之，
// 生产 PG 上首次 INSERT/SELECT 会报 column does not exist。
// 本测试在 SQLite 中以补齐列的表来验证往返（与 model 定义一致）。
func TestRepository_CreateAndGet(t *testing.T) {
	r := newTestRepo(t)
	req := mkReq("req_aaa", "ps_1")
	mustCreateRepo(t, r, req)

	got, err := r.Get(context.Background(), "req_aaa")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != "req_aaa" || got.Title != "登录页改造" {
		t.Fatalf("Get 返回不匹配: %+v", got)
	}
	if got.Status != "specified" || got.Priority != "P1" || got.FixedVersion != "v1.2" {
		t.Fatalf("枚举字段往返不一致: status=%s priority=%s fixed=%s", got.Status, got.Priority, got.FixedVersion)
	}
	if got.AcceptanceCriteria != `["点击 SSO 跳转","回调后自动登录"]` {
		t.Fatalf("AcceptanceCriteria 往返不一致: %s", got.AcceptanceCriteria)
	}
	// Create 不写 tasks/assignee/assigned_at → 读回应为零值
	if got.Tasks != "" || got.Assignee != "" || got.AssignedAt != nil {
		t.Fatalf("新建需求 tasks/assignee/assigned_at 应为零值，得到 tasks=%q assignee=%q assigned_at=%v",
			got.Tasks, got.Assignee, got.AssignedAt)
	}
}

// TestRepository_Get_NotFound 查询不存在的 id 应返回 sql.ErrNoRows（调用方据此判"不存在"）。
func TestRepository_Get_NotFound(t *testing.T) {
	r := newTestRepo(t)
	_, err := r.Get(context.Background(), "req_missing")
	if err == nil {
		t.Fatal("查询不存在的需求应返回 error（sql.ErrNoRows）")
	}
}

// TestRepository_List 按 project_space_id 过滤，按 created_at DESC 排序（最新优先）。
func TestRepository_List(t *testing.T) {
	r := newTestRepo(t)
	mustCreateRepo(t, r, mkReq("req_old", "ps_1"))
	// 错开 created_at：第二条显式延后 1 秒，保证 DESC 顺序稳定
	time.Sleep(time.Second + 100*time.Millisecond)
	mustCreateRepo(t, r, mkReq("req_new", "ps_1"))
	mustCreateRepo(t, r, mkReq("req_other", "ps_2")) // 不同空间，不应出现

	got, err := r.List(context.Background(), "ps_1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ps_1 应返回 2 条，得到 %d", len(got))
	}
	if got[0].ID != "req_new" || got[1].ID != "req_old" {
		t.Fatalf("List 应按 created_at DESC，得到 %s,%s", got[0].ID, got[1].ID)
	}
}

// TestRepository_List_Empty 无数据时返回空切片、无 error。
func TestRepository_List_Empty(t *testing.T) {
	r := newTestRepo(t)
	got, err := r.List(context.Background(), "ps_empty")
	if err != nil {
		t.Fatalf("list empty: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("空项目空间应返回 0 条，得到 %d", len(got))
	}
}

// TestRepository_ListByApp 按应用维度筛选需求（应用一等公民：应用拥有需求池）。
func TestRepository_ListByApp(t *testing.T) {
	r := newTestRepo(t)
	for _, c := range []struct{ id, appID string }{
		{"req_a1", "app_x"},
		{"req_a2", "app_x"},
		{"req_a3", "app_y"},
		{"req_a4", ""}, // 未归属应用
	} {
		req := mkReq(c.id, "ps_1")
		req.ApplicationID = c.appID
		mustCreateRepo(t, r, req)
	}

	got, err := r.ListByApp(context.Background(), "app_x")
	if err != nil {
		t.Fatalf("ListByApp: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("app_x 应返回 2 条，得到 %d", len(got))
	}
	for _, q := range got {
		if q.ApplicationID != "app_x" {
			t.Fatalf("ListByApp 不应返回其他应用的需求: %+v", q)
		}
	}
}

// TestRepository_UpdateStatus 更新状态（生命周期：specified→developing→delivered）。
func TestRepository_UpdateStatus(t *testing.T) {
	r := newTestRepo(t)
	mustCreateRepo(t, r, mkReq("req_s", "ps_1"))

	if err := r.UpdateStatus(context.Background(), "req_s", "developing"); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	got, _ := r.Get(context.Background(), "req_s")
	if got.Status != "developing" {
		t.Fatalf("status 应为 developing，得到 %s", got.Status)
	}
}

// TestRepository_SetApplication 把需求归属到应用（发布后回填 application_id）。
func TestRepository_SetApplication(t *testing.T) {
	r := newTestRepo(t)
	mustCreateRepo(t, r, mkReq("req_app", "ps_1"))

	if err := r.SetApplication(context.Background(), "req_app", "app_new"); err != nil {
		t.Fatalf("SetApplication: %v", err)
	}
	got, _ := r.Get(context.Background(), "req_app")
	if got.ApplicationID != "app_new" {
		t.Fatalf("application_id 应为 app_new，得到 %s", got.ApplicationID)
	}
}

// TestRepository_UpdateTasks 子任务 JSON 往返一致（[{text,done}] 结构）。
func TestRepository_UpdateTasks(t *testing.T) {
	r := newTestRepo(t)
	mustCreateRepo(t, r, mkReq("req_t", "ps_1"))

	tasks := `[{"text":"搭脚手架","done":true},{"text":"写测试","done":false}]`
	if err := r.UpdateTasks(context.Background(), "req_t", tasks); err != nil {
		t.Fatalf("UpdateTasks: %v", err)
	}
	got, _ := r.Get(context.Background(), "req_t")
	if got.Tasks != tasks {
		t.Fatalf("tasks 往返不一致:\n got: %s\nwant: %s", got.Tasks, tasks)
	}
}

// ===== 认领互斥（重点）=====

// TestRepository_Assign_Unclaimed 未认领→认领成功，assignee+assigned_at 落库。
func TestRepository_Assign_Unclaimed(t *testing.T) {
	r := newTestRepo(t)
	mustCreateRepo(t, r, mkReq("req_u", "ps_1"))

	if err := r.Assign(context.Background(), "req_u", "alice"); err != nil {
		t.Fatalf("Assign 未认领应成功: %v", err)
	}
	got, _ := r.Get(context.Background(), "req_u")
	if got.Assignee != "alice" {
		t.Fatalf("assignee 应为 alice，得到 %s", got.Assignee)
	}
	if got.AssignedAt == nil || got.AssignedAt.IsZero() {
		t.Fatal("assigned_at 应被写入非零时间")
	}
}

// TestRepository_Assign_AlreadyClaimedByOther 已被他人认领→拒绝（含当前认领人）。
func TestRepository_Assign_AlreadyClaimedByOther(t *testing.T) {
	r := newTestRepo(t)
	mustCreateRepo(t, r, mkReq("req_lock", "ps_1"))

	// alice 先认领
	if err := r.Assign(context.Background(), "req_lock", "alice"); err != nil {
		t.Fatalf("alice 首次认领应成功: %v", err)
	}
	// bob 后认领应被拒，错误信息含 alice
	err := r.Assign(context.Background(), "req_lock", "bob")
	if err == nil {
		t.Fatal("已被他人认领时 Assign 应返回 error")
	}
	if !strings.Contains(err.Error(), "alice") {
		t.Fatalf("错误信息应含当前认领人 alice，得到: %v", err)
	}
	// 库中认领人未被覆盖
	got, _ := r.Get(context.Background(), "req_lock")
	if got.Assignee != "alice" {
		t.Fatalf("互斥失败：assignee 被覆盖为 %s", got.Assignee)
	}
}

// TestRepository_Assign_IdempotentSelf 本人重复认领→幂等成功（不报错、assigned_at 刷新）。
func TestRepository_Assign_IdempotentSelf(t *testing.T) {
	r := newTestRepo(t)
	mustCreateRepo(t, r, mkReq("req_idem", "ps_1"))

	if err := r.Assign(context.Background(), "req_idem", "alice"); err != nil {
		t.Fatalf("首次认领: %v", err)
	}
	first, _ := r.Get(context.Background(), "req_idem")
	firstAt := first.AssignedAt

	// 等待 1.1s 后本人再认领，验证幂等 + assigned_at 刷新
	time.Sleep(1100 * time.Millisecond)
	if err := r.Assign(context.Background(), "req_idem", "alice"); err != nil {
		t.Fatalf("本人重复认领应幂等成功: %v", err)
	}
	second, _ := r.Get(context.Background(), "req_idem")
	if second.Assignee != "alice" {
		t.Fatalf("幂等认领后 assignee 仍是 alice，得到 %s", second.Assignee)
	}
	if firstAt == nil || second.AssignedAt == nil || !second.AssignedAt.After(*firstAt) {
		t.Fatalf("重复认领应刷新 assigned_at，first=%v second=%v", firstAt, second.AssignedAt)
	}
}

// TestRepository_Release_ClearsAssigneeAndAssignedAt 释放后 assignee 置空、assigned_at 置 NULL。
func TestRepository_Release_ClearsAssigneeAndAssignedAt(t *testing.T) {
	r := newTestRepo(t)
	mustCreateRepo(t, r, mkReq("req_rel", "ps_1"))
	mustAssign(t, r, "req_rel", "alice")

	if err := r.Release(context.Background(), "req_rel"); err != nil {
		t.Fatalf("Release: %v", err)
	}
	got, _ := r.Get(context.Background(), "req_rel")
	if got.Assignee != "" {
		t.Fatalf("Release 后 assignee 应为空，得到 %s", got.Assignee)
	}
	if got.AssignedAt != nil {
		t.Fatalf("Release 后 assigned_at 应为 nil，得到 %v", got.AssignedAt)
	}
}

// TestRepository_Assign_AfterRelease_OtherCanClaim 释放后他人可认领（互斥锁释放）。
// 覆盖场景：alice 认领 → 释放 → bob 认领成功。
func TestRepository_Assign_AfterRelease_OtherCanClaim(t *testing.T) {
	r := newTestRepo(t)
	mustCreateRepo(t, r, mkReq("req_handover", "ps_1"))

	mustAssign(t, r, "req_handover", "alice")
	if err := r.Release(context.Background(), "req_handover"); err != nil {
		t.Fatalf("Release: %v", err)
	}
	// bob 现在应能认领
	if err := r.Assign(context.Background(), "req_handover", "bob"); err != nil {
		t.Fatalf("释放后他人认领应成功: %v", err)
	}
	got, _ := r.Get(context.Background(), "req_handover")
	if got.Assignee != "bob" {
		t.Fatalf("释放后 bob 应认领成功，得到 %s", got.Assignee)
	}
}

// TestRepository_Release_Idempotent 对未认领/已释放的需求再调用 Release 不报错（清理动作幂等）。
func TestRepository_Release_Idempotent(t *testing.T) {
	r := newTestRepo(t)
	mustCreateRepo(t, r, mkReq("req_rel2", "ps_1"))

	if err := r.Release(context.Background(), "req_rel2"); err != nil {
		t.Fatalf("对未认领需求 Release 应幂等无错: %v", err)
	}
}

// TestRepository_Assign_NotFound 对不存在的需求 Assign：UPDATE 0 行 → 返回"已被 认领"。
// 这是当前实现的小毛病：找不到时错误信息会把认领人显示为空字符串，语义模糊。
// 测试固定此行为，便于将来优化（如改为"需求不存在"）时回归。
func TestRepository_Assign_NotFound(t *testing.T) {
	r := newTestRepo(t)
	err := r.Assign(context.Background(), "req_missing", "alice")
	if err == nil {
		t.Fatal("对不存在的需求 Assign 应返回 error")
	}
	// 当前实现：错误信息为"需求已被  认领"（中间空白），语义不准确——记入潜在 bug。
	if !strings.Contains(err.Error(), "认领") {
		t.Fatalf("错误信息应包含'认领'，得到: %v", err)
	}
}

// mustAssign 认领并断言成功。
func mustAssign(t *testing.T, r *Repository, id, user string) {
	t.Helper()
	if err := r.Assign(context.Background(), id, user); err != nil {
		t.Fatalf("assign %s to %s: %v", id, user, err)
	}
}
