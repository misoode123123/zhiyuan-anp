package attendance

import (
	"context"

	"github.com/jmoiron/sqlx"
)

// Store 考勤数据访问。
type Store struct {
	db *sqlx.DB
}

// NewStore 构造 Store。
func NewStore(db *sqlx.DB) *Store { return &Store{db: db} }

// Create 新建考勤记录。
func (s *Store) Create(ctx context.Context, rec *AttendanceRecord) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO attendance_record (id, project_space_id, user_id, status, start_time, end_time, reason, supervisor_id, approval_status)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		rec.ID, rec.ProjectSpaceID, rec.UserID, rec.Status, rec.StartTime, rec.EndTime, rec.Reason, rec.SupervisorID, rec.ApprovalStatus)
	return err
}

// ListByProjectSpace 列出项目空间下的考勤记录。
func (s *Store) ListByProjectSpace(ctx context.Context, projectSpaceID string) ([]AttendanceRecord, error) {
	var list []AttendanceRecord
	err := s.db.SelectContext(ctx, &list,
		`SELECT id, project_space_id, user_id, status, start_time, end_time, COALESCE(reason,'') AS reason, supervisor_id, approval_status, COALESCE(approver,'') AS approver, approved_at, created_at, updated_at FROM attendance_record WHERE project_space_id = $1 ORDER BY created_at DESC`, projectSpaceID)
	return list, err
}

// ListByUser 列出某员工的考勤记录。
func (s *Store) ListByUser(ctx context.Context, projectSpaceID, userID string) ([]AttendanceRecord, error) {
	var list []AttendanceRecord
	err := s.db.SelectContext(ctx, &list,
		`SELECT id, project_space_id, user_id, status, start_time, end_time, COALESCE(reason,'') AS reason, supervisor_id, approval_status, COALESCE(approver,'') AS approver, approved_at, created_at, updated_at FROM attendance_record WHERE project_space_id = $1 AND user_id = $2 ORDER BY created_at DESC`,
		projectSpaceID, userID)
	return list, err
}

// ListBySupervisor 列出待某上级审批的考勤记录（approvalStatus 为空则不限）。
func (s *Store) ListBySupervisor(ctx context.Context, supervisorID, approvalStatus string) ([]AttendanceRecord, error) {
	q := `SELECT id, project_space_id, user_id, status, start_time, end_time, COALESCE(reason,'') AS reason, supervisor_id, approval_status, COALESCE(approver,'') AS approver, approved_at, created_at, updated_at FROM attendance_record WHERE supervisor_id = $1`
	args := []interface{}{supervisorID}
	if approvalStatus != "" {
		q += ` AND approval_status = ?`
		args = append(args, approvalStatus)
	}
	q += ` ORDER BY created_at DESC`
	var list []AttendanceRecord
	err := s.db.SelectContext(ctx, &list, q, args...)
	return list, err
}

// Get 查单条考勤记录。
func (s *Store) Get(ctx context.Context, id string) (*AttendanceRecord, error) {
	var rec AttendanceRecord
	err := s.db.GetContext(ctx, &rec, `SELECT id, project_space_id, user_id, status, start_time, end_time, COALESCE(reason,'') AS reason, supervisor_id, approval_status, COALESCE(approver,'') AS approver, approved_at, created_at, updated_at FROM attendance_record WHERE id = $1`, id)
	return &rec, err
}

// UpdateApproval 更新审批状态（登记审批人与时间）。
func (s *Store) UpdateApproval(ctx context.Context, id, approvalStatus, approver string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE attendance_record SET approval_status = $1, approver = $2, approved_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP WHERE id = $3`,
		approvalStatus, approver, id)
	return err
}
