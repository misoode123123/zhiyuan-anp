package codetask

import (
	"context"

	"github.com/jmoiron/sqlx"
)

// Store 异步编码任务数据访问。
type Store struct {
	db *sqlx.DB
}

// NewStore 构造 Store。
func NewStore(db *sqlx.DB) *Store { return &Store{db: db} }

// Create 新建任务（running）。
func (s *Store) Create(ctx context.Context, t *Task) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO code_task (id, project_space_id, kind, source_id, repo_dir, prompt, model, status)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, 'running')`,
		t.ID, t.ProjectSpaceID, t.Kind, t.SourceID, t.RepoDir, t.Prompt, t.Model)
	return err
}

// Get 读取任务。
func (s *Store) Get(ctx context.Context, id string) (*Task, error) {
	var t Task
	err := s.db.GetContext(ctx, &t,
		`SELECT id, project_space_id, kind, source_id, repo_dir, prompt, model, status, output, change_id, created_at, updated_at
		 FROM code_task WHERE id = $1`, id)
	return &t, err
}

// ListByProjectSpace 列出项目空间的任务。
func (s *Store) ListByProjectSpace(ctx context.Context, projectSpaceID string) ([]Task, error) {
	var list []Task
	err := s.db.SelectContext(ctx, &list,
		`SELECT t.id, t.project_space_id, t.kind, t.source_id, t.repo_dir, t.prompt, t.model, t.status, t.output, t.change_id, t.created_at, t.updated_at,
		        COALESCE(r.title,'') AS req_title,
		        COALESCE(a.name,'') AS app_name
		 FROM code_task t
		 LEFT JOIN requirement r ON r.id = t.source_id
		 LEFT JOIN change_request ch ON ch.id = t.change_id
		 LEFT JOIN appdeploy_application a ON a.id = ch.source_id OR a.id IN (SELECT application_id FROM requirement WHERE id = ch.source_id)
		 WHERE t.project_space_id = $1 ORDER BY t.created_at DESC LIMIT 100`, projectSpaceID)
	return list, err
}

// MarkCompleted 标记完成 + 产出。
func (s *Store) MarkCompleted(ctx context.Context, id, output string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE code_task SET status='completed', output=$1, updated_at=CURRENT_TIMESTAMP WHERE id=$2`, output, id)
	return err
}

// MarkFailed 标记失败 + 错误信息。
func (s *Store) MarkFailed(ctx context.Context, id, output string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE code_task SET status='failed', output=$1, updated_at=CURRENT_TIMESTAMP WHERE id=$2`, output, id)
	return err
}

// SetChangeID 回填登记的变更 ID。
func (s *Store) SetChangeID(ctx context.Context, id, changeID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE code_task SET change_id=$1 WHERE id=$2`, changeID, id)
	return err
}
