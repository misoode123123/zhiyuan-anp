package attendance

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Service 考勤业务逻辑：校验入参 → 入库 → 转直接上级审批闭环。
type Service struct {
	store *Store
}

// NewService 构造 Service。
func NewService(store *Store) *Service { return &Service{store: store} }

// validStatuses 允许的考勤状态集合（验收标准 1）。
var validStatuses = map[string]struct{}{StatusRest: {}, StatusOvertime: {}, StatusLeave: {}}

// SubmitInput 提交考勤入参。
type SubmitInput struct {
	ProjectSpaceID string
	UserID         string
	Status         string
	StartTime      time.Time
	EndTime        time.Time
	Reason         string
	SupervisorID   string
}

// Submit 校验并提交考勤，记录状态 pending 等待上级审批（验收标准 2、3）。
func (s *Service) Submit(ctx context.Context, in SubmitInput) (*AttendanceRecord, error) {
	if _, ok := validStatuses[in.Status]; !ok {
		return nil, fmt.Errorf("非法考勤状态 %q（仅允许 rest/overtime/leave）", in.Status)
	}
	if in.UserID == "" {
		return nil, fmt.Errorf("缺少员工标识")
	}
	if in.SupervisorID == "" {
		return nil, fmt.Errorf("缺少直接上级标识")
	}
	if in.EndTime.Before(in.StartTime) || in.EndTime.Equal(in.StartTime) {
		return nil, fmt.Errorf("结束时间必须晚于起始时间")
	}
	rec := &AttendanceRecord{
		ID:             "att_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20],
		ProjectSpaceID: in.ProjectSpaceID,
		UserID:         in.UserID,
		Status:         in.Status,
		StartTime:      in.StartTime,
		EndTime:        in.EndTime,
		Reason:         in.Reason,
		SupervisorID:   in.SupervisorID,
		ApprovalStatus: ApprovalPending,
	}
	if err := s.store.Create(ctx, rec); err != nil {
		return nil, err
	}
	return rec, nil
}

// List 列出项目空间下的考勤记录。
func (s *Service) List(ctx context.Context, projectSpaceID string) ([]AttendanceRecord, error) {
	return s.store.ListByProjectSpace(ctx, projectSpaceID)
}

// ListMine 列出当前员工的考勤记录。
func (s *Service) ListMine(ctx context.Context, projectSpaceID, userID string) ([]AttendanceRecord, error) {
	return s.store.ListByUser(ctx, projectSpaceID, userID)
}

// Inbox 列出待某上级审批的考勤记录（默认 pending，验收标准 4）。
func (s *Service) Inbox(ctx context.Context, supervisorID, approvalStatus string) ([]AttendanceRecord, error) {
	if approvalStatus == "" {
		approvalStatus = ApprovalPending
	}
	return s.store.ListBySupervisor(ctx, supervisorID, approvalStatus)
}

// Approve 审批通过（仅直接上级可操作）。
func (s *Service) Approve(ctx context.Context, id, approver string) (*AttendanceRecord, error) {
	return s.decide(ctx, id, approver, ApprovalApproved)
}

// Reject 审批驳回（仅直接上级可操作）。
func (s *Service) Reject(ctx context.Context, id, approver string) (*AttendanceRecord, error) {
	return s.decide(ctx, id, approver, ApprovalRejected)
}

func (s *Service) decide(ctx context.Context, id, approver, decision string) (*AttendanceRecord, error) {
	rec, err := s.store.Get(ctx, id)
	if err != nil || rec == nil || rec.ID == "" {
		return nil, fmt.Errorf("考勤记录 %s 不存在", id)
	}
	if rec.ApprovalStatus != ApprovalPending {
		return nil, fmt.Errorf("考勤记录已处理（%s），不可重复审批", rec.ApprovalStatus)
	}
	if rec.SupervisorID != approver {
		return nil, fmt.Errorf("仅直接上级可审批此考勤")
	}
	if err := s.store.UpdateApproval(ctx, id, decision, approver); err != nil {
		return nil, err
	}
	rec.ApprovalStatus = decision
	rec.Approver = approver
	return rec, nil
}
