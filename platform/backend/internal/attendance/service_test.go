package attendance

import (
	"context"
	"strings"
	"testing"
	"time"
)

// newSvc 用内存 store 构造 Service（仅业务逻辑，不碰 HTTP）。
func newSvc(t *testing.T) *Service {
	t.Helper()
	return NewService(newTestStore(t))
}

// sub 输入便捷构造器；start/end 由调用方按场景覆盖。
func sub(status string) SubmitInput {
	start := time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC)
	return SubmitInput{
		ProjectSpaceID: "ps_1",
		UserID:         "u_alice",
		Status:         status,
		StartTime:      start,
		EndTime:        start.Add(8 * time.Hour),
		Reason:         "事由",
		SupervisorID:   "sup_bob",
	}
}

// TestSubmit_Success happy path：合法入参入库，approval_status=pending、ID 形如 att_。
func TestSubmit_Success(t *testing.T) {
	svc := newSvc(t)
	rec, err := svc.Submit(context.Background(), sub(StatusOvertime))
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if !strings.HasPrefix(rec.ID, "att_") {
		t.Fatalf("ID 应以 att_ 开头，得到 %s", rec.ID)
	}
	if rec.ApprovalStatus != ApprovalPending {
		t.Fatalf("新提交应为 pending，得到 %s", rec.ApprovalStatus)
	}
	if rec.Status != StatusOvertime {
		t.Fatalf("status 应原样保存，得到 %s", rec.Status)
	}
}

// TestSubmit_InvalidStatus 非法考勤状态被拒。
func TestSubmit_InvalidStatus(t *testing.T) {
	svc := newSvc(t)
	in := sub("vacation") // rest/overtime/leave 之外
	_, err := svc.Submit(context.Background(), in)
	if err == nil || !strings.Contains(err.Error(), "非法考勤状态") {
		t.Fatalf("应拒绝非法状态，得到 %v", err)
	}
}

// TestSubmit_StatusBoundary 三种合法状态边界全覆盖。
func TestSubmit_StatusBoundary(t *testing.T) {
	for _, st := range []string{StatusRest, StatusOvertime, StatusLeave} {
		svc := newSvc(t)
		rec, err := svc.Submit(context.Background(), sub(st))
		if err != nil {
			t.Fatalf("status=%s 应成功，得到 %v", st, err)
		}
		if rec.Status != st {
			t.Errorf("status=%s 未原样保存", st)
		}
	}
}

// TestSubmit_EndBeforeStart 结束早于起始应被拒。
func TestSubmit_EndBeforeStart(t *testing.T) {
	svc := newSvc(t)
	in := sub(StatusLeave)
	in.EndTime = in.StartTime.Add(-time.Hour)
	_, err := svc.Submit(context.Background(), in)
	if err == nil || !strings.Contains(err.Error(), "结束时间必须晚于起始时间") {
		t.Fatalf("应拒绝 end<start，得到 %v", err)
	}
}

// TestSubmit_EndEqualStart 结束等于起始也应被拒（边界：不可等于）。
func TestSubmit_EndEqualStart(t *testing.T) {
	svc := newSvc(t)
	in := sub(StatusLeave)
	in.EndTime = in.StartTime
	_, err := svc.Submit(context.Background(), in)
	if err == nil || !strings.Contains(err.Error(), "结束时间必须晚于起始时间") {
		t.Fatalf("应拒绝 end==start，得到 %v", err)
	}
}

// TestSubmit_MissingUserID 缺员工标识拒绝。
func TestSubmit_MissingUserID(t *testing.T) {
	svc := newSvc(t)
	in := sub(StatusRest)
	in.UserID = ""
	_, err := svc.Submit(context.Background(), in)
	if err == nil || !strings.Contains(err.Error(), "缺少员工标识") {
		t.Fatalf("应拒绝空 UserID，得到 %v", err)
	}
}

// TestSubmit_MissingSupervisorID 缺直接上级拒绝（验收标准 4 转上级审批的前提）。
func TestSubmit_MissingSupervisorID(t *testing.T) {
	svc := newSvc(t)
	in := sub(StatusRest)
	in.SupervisorID = ""
	_, err := svc.Submit(context.Background(), in)
	if err == nil || !strings.Contains(err.Error(), "缺少直接上级标识") {
		t.Fatalf("应拒绝空 SupervisorID，得到 %v", err)
	}
}

// TestApproveSuccess 审批通过：pending→approved，approver 落库。
func TestApproveSuccess(t *testing.T) {
	svc := newSvc(t)
	rec, _ := svc.Submit(context.Background(), sub(StatusRest))
	got, err := svc.Approve(context.Background(), rec.ID, "sup_bob")
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if got.ApprovalStatus != ApprovalApproved {
		t.Fatalf("应为 approved，得到 %s", got.ApprovalStatus)
	}
	if got.Approver != "sup_bob" {
		t.Fatalf("approver 应为 sup_bob，得到 %s", got.Approver)
	}
}

// TestRejectSuccess 审批驳回：pending→rejected。
func TestRejectSuccess(t *testing.T) {
	svc := newSvc(t)
	rec, _ := svc.Submit(context.Background(), sub(StatusRest))
	got, err := svc.Reject(context.Background(), rec.ID, "sup_bob")
	if err != nil {
		t.Fatalf("reject: %v", err)
	}
	if got.ApprovalStatus != ApprovalRejected {
		t.Fatalf("应为 rejected，得到 %s", got.ApprovalStatus)
	}
}

// TestDecide_AlreadyDecided 已决记录不可重复审批（状态机：pending→approved 后不能再改）。
func TestDecide_AlreadyDecided(t *testing.T) {
	svc := newSvc(t)
	rec, _ := svc.Submit(context.Background(), sub(StatusRest))
	if _, err := svc.Approve(context.Background(), rec.ID, "sup_bob"); err != nil {
		t.Fatalf("首次 approve: %v", err)
	}
	// 再次 approve
	_, err := svc.Approve(context.Background(), rec.ID, "sup_bob")
	if err == nil || !strings.Contains(err.Error(), "不可重复审批") {
		t.Fatalf("重复审批应被拒，得到 %v", err)
	}
	// 再次改 reject 也不行（pending→approved 后状态锁死）
	_, err = svc.Reject(context.Background(), rec.ID, "sup_bob")
	if err == nil || !strings.Contains(err.Error(), "不可重复审批") {
		t.Fatalf("approved 后改 rejected 应被拒，得到 %v", err)
	}
}

// TestDecide_WrongApprover 仅直接上级可审批，他人操作应被拒。
func TestDecide_WrongApprover(t *testing.T) {
	svc := newSvc(t)
	rec, _ := svc.Submit(context.Background(), sub(StatusRest))
	_, err := svc.Approve(context.Background(), rec.ID, "sup_eve") // 不是 sup_bob
	if err == nil || !strings.Contains(err.Error(), "仅直接上级可审批") {
		t.Fatalf("非直接上级应被拒，得到 %v", err)
	}
}

// TestDecide_NotFound 审批不存在记录应明确报错。
func TestDecide_NotFound(t *testing.T) {
	svc := newSvc(t)
	_, err := svc.Approve(context.Background(), "att_nope", "sup_bob")
	if err == nil || !strings.Contains(err.Error(), "不存在") {
		t.Fatalf("审批不存在记录应报错，得到 %v", err)
	}
}

// TestListMine 「我的考勤」仅返回当前用户的记录。
func TestListMine(t *testing.T) {
	svc := newSvc(t)
	_, _ = svc.Submit(context.Background(), sub(StatusRest))
	in2 := sub(StatusOvertime)
	in2.UserID = "u_bob"
	_, _ = svc.Submit(context.Background(), in2)

	got, err := svc.ListMine(context.Background(), "ps_1", "u_alice")
	if err != nil {
		t.Fatalf("list mine: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("u_alice 应只 1 条，得到 %d", len(got))
	}
	if got[0].UserID != "u_alice" {
		t.Fatalf("ListMine 返回非 alice 记录")
	}
}

// TestInbox_DefaultPending 默认只返回 pending，已决的不在收件箱。
func TestInbox_DefaultPending(t *testing.T) {
	svc := newSvc(t)
	a, _ := svc.Submit(context.Background(), sub(StatusRest))
	_, _ = svc.Submit(context.Background(), sub(StatusOvertime)) // 始终 pending
	_, _ = svc.Approve(context.Background(), a.ID, "sup_bob")    // a 变 approved

	got, err := svc.Inbox(context.Background(), "sup_bob", "")
	if err != nil {
		t.Fatalf("inbox: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("默认 inbox 应仅 1 条 pending，得到 %d", len(got))
	}
	if got[0].ApprovalStatus != ApprovalPending {
		t.Fatalf("默认 inbox 只应有 pending，得到 %s", got[0].ApprovalStatus)
	}
}

// TestInbox_ExplicitStatus 显式指定 status 时按其过滤（approved 也可见）。
func TestInbox_ExplicitStatus(t *testing.T) {
	svc := newSvc(t)
	a, _ := svc.Submit(context.Background(), sub(StatusRest))
	_, _ = svc.Approve(context.Background(), a.ID, "sup_bob")

	got, err := svc.Inbox(context.Background(), "sup_bob", ApprovalApproved)
	if err != nil {
		t.Fatalf("inbox approved: %v", err)
	}
	if len(got) != 1 || got[0].ID != a.ID {
		t.Fatalf("approved inbox 应只含 a，得到 %+v", got)
	}
}

// TestList 「项目空间列表」返回该空间所有记录，跨用户。
func TestList(t *testing.T) {
	svc := newSvc(t)
	_, _ = svc.Submit(context.Background(), sub(StatusRest))
	in2 := sub(StatusOvertime)
	in2.UserID = "u_bob"
	_, _ = svc.Submit(context.Background(), in2)

	got, err := svc.List(context.Background(), "ps_1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ps_1 应有 2 条，得到 %d", len(got))
	}
}
