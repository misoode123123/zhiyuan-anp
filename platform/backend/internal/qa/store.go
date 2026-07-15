package qa

import (
	"context"
	"time"

	"github.com/jmoiron/sqlx"
)

// Store 测试用例数据访问。
type Store struct {
	db *sqlx.DB
}

// NewStore 构造 Store。
func NewStore(db *sqlx.DB) *Store { return &Store{db: db} }

// tcCols 显式列（可空文本/数值列 COALESCE 防 NULL→string/int 扫描错误）。
const tcCols = `id, project_space_id, COALESCE(requirement_id,'') AS requirement_id, title, COALESCE(steps,'') AS steps, COALESCE(expected,'') AS expected, status, COALESCE(method,'') AS method, COALESCE(path,'') AS path, COALESCE(expected_status,0) AS expected_status, COALESCE(expected_body,'') AS expected_body, COALESCE(actual_status,0) AS actual_status, COALESCE(actual_body,'') AS actual_body, run_at, created_at`

// Create 新建测试用例。
func (s *Store) Create(ctx context.Context, tc *TestCase) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO test_case (id, project_space_id, requirement_id, title, steps, expected, status, method, path, expected_status, expected_body)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		tc.ID, tc.ProjectSpaceID, tc.RequirementID, tc.Title, tc.Steps, tc.Expected, tc.Status,
		tc.Method, tc.Path, tc.ExpectedStatus, tc.ExpectedBody)
	return err
}

// Get 取单条。
func (s *Store) Get(ctx context.Context, id string) (*TestCase, error) {
	var tc TestCase
	err := s.db.GetContext(ctx, &tc, `SELECT `+tcCols+` FROM test_case WHERE id = ?`, id)
	return &tc, err
}

// ListByProjectSpace 列出项目空间下的测试用例。
func (s *Store) ListByProjectSpace(ctx context.Context, projectSpaceID string) ([]TestCase, error) {
	var list []TestCase
	err := s.db.SelectContext(ctx, &list,
		`SELECT `+tcCols+` FROM test_case WHERE project_space_id = ? ORDER BY created_at DESC`, projectSpaceID)
	return list, err
}

// ListByRequirement 列出某需求下的测试用例（批量运行用）。
func (s *Store) ListByRequirement(ctx context.Context, requirementID string) ([]TestCase, error) {
	var list []TestCase
	err := s.db.SelectContext(ctx, &list,
		`SELECT `+tcCols+` FROM test_case WHERE requirement_id = ? ORDER BY created_at DESC`, requirementID)
	return list, err
}

// UpdateRun 回写运行结果（状态 + 实际状态码/响应 + 运行时间）。
func (s *Store) UpdateRun(ctx context.Context, tc *TestCase) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE test_case SET status = ?, actual_status = ?, actual_body = ?, run_at = ? WHERE id = ?`,
		tc.Status, tc.ActualStatus, tc.ActualBody, tc.RunAt, tc.ID)
	return err
}

// nowTime 当前时间指针（便于 RunAt 赋值；抽出便于测试）。
func nowTime() *time.Time { t := time.Now(); return &t }
