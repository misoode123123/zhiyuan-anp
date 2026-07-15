// Package attendance 是「考勤管理」限界上下文 —— 员工提交考勤（休息/加班/请假）
// 并转发直接上级审批，形成考勤提交→审批闭环。
package attendance

import "time"

// 考勤状态（验收标准 1：选择考勤状态）。
const (
	StatusRest     = "rest"     // 休息
	StatusOvertime = "overtime" // 加班
	StatusLeave    = "leave"    // 请假
)

// 审批状态。
const (
	ApprovalPending  = "pending"
	ApprovalApproved = "approved"
	ApprovalRejected = "rejected"
)

// AttendanceRecord 考勤记录（验收标准 3：系统记录考勤信息）。
type AttendanceRecord struct {
	ID             string     `json:"id" db:"id"`
	ProjectSpaceID string     `json:"project_space_id" db:"project_space_id"`
	UserID         string     `json:"user_id" db:"user_id"`
	Status         string     `json:"status" db:"status"` // rest/overtime/leave
	StartTime      time.Time  `json:"start_time" db:"start_time"`
	EndTime        time.Time  `json:"end_time" db:"end_time"`
	Reason         string     `json:"reason,omitempty" db:"reason"`
	SupervisorID   string     `json:"supervisor_id" db:"supervisor_id"` // 直接上级（验收标准 4：转直接上级）
	ApprovalStatus string     `json:"approval_status" db:"approval_status"`
	Approver       string     `json:"approver,omitempty" db:"approver"`
	ApprovedAt     *time.Time `json:"approved_at,omitempty" db:"approved_at"`
	CreatedAt      time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at" db:"updated_at"`
}
