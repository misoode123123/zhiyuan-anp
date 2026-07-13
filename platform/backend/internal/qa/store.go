package qa

import (
	"context"

	"github.com/jmoiron/sqlx"
)

// Store 测试用例数据访问。
type Store struct {
	db *sqlx.DB
}

// NewStore 构造 Store。
func NewStore(db *sqlx.DB) *Store { return &Store{db: db} }

// Create 新建测试用例。
func (s *Store) Create(ctx context.Context, tc *TestCase) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO test_case (id, project_space_id, requirement_id, title, steps, expected, status)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		tc.ID, tc.ProjectSpaceID, tc.RequirementID, tc.Title, tc.Steps, tc.Expected, tc.Status)
	return err
}

// ListByProjectSpace 列出项目空间下的测试用例。
func (s *Store) ListByProjectSpace(ctx context.Context, projectSpaceID string) ([]TestCase, error) {
	var list []TestCase
	err := s.db.SelectContext(ctx, &list,
		`SELECT id, project_space_id, requirement_id, title, steps, expected, status, created_at
		 FROM test_case WHERE project_space_id = ? ORDER BY created_at DESC`, projectSpaceID)
	return list, err
}
