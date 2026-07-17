package requirement

import (
	"context"

	"github.com/jmoiron/sqlx"
)

// Repository 需求数据访问。
type Repository struct {
	db *sqlx.DB
}

// NewRepository 构造 Repository。
func NewRepository(db *sqlx.DB) *Repository { return &Repository{db: db} }

// reqCols 显式列（application_id 可空，用 COALESCE 避免 NULL→string 扫描错误）。
const reqCols = `id, project_space_id, COALESCE(application_id,'') AS application_id, title, description, user_story, acceptance_criteria, status, COALESCE(priority,'') AS priority, COALESCE(fixed_version,'') AS fixed_version, COALESCE(tasks,'') AS tasks, created_at, updated_at`

// UpdateTasks 更新需求的子任务清单(JSON)。
func (r *Repository) UpdateTasks(ctx context.Context, id, tasks string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE requirement SET tasks = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, tasks, id)
	return err
}

func (r *Repository) Create(ctx context.Context, req *Requirement) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO requirement (id, project_space_id, application_id, title, description, user_story, acceptance_criteria, status, priority, fixed_version)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		req.ID, req.ProjectSpaceID, req.ApplicationID, req.Title, req.Description, req.UserStory, req.AcceptanceCriteria, req.Status, req.Priority, req.FixedVersion)
	return err
}

func (r *Repository) List(ctx context.Context, projectSpaceID string) ([]Requirement, error) {
	var list []Requirement
	err := r.db.SelectContext(ctx, &list,
		`SELECT `+reqCols+` FROM requirement WHERE project_space_id = ? ORDER BY created_at DESC`, projectSpaceID)
	return list, err
}

// ListByApp 列出某应用下的需求（应用一等公民：应用拥有需求池）。
func (r *Repository) ListByApp(ctx context.Context, appID string) ([]Requirement, error) {
	var list []Requirement
	err := r.db.SelectContext(ctx, &list,
		`SELECT `+reqCols+` FROM requirement WHERE application_id = ? ORDER BY created_at DESC`, appID)
	return list, err
}

func (r *Repository) Get(ctx context.Context, id string) (*Requirement, error) {
	var req Requirement
	err := r.db.GetContext(ctx, &req, `SELECT `+reqCols+` FROM requirement WHERE id = ?`, id)
	return &req, err
}

// UpdateStatus 更新需求状态（发布后→delivered，闭环需求生命周期）。
func (r *Repository) UpdateStatus(ctx context.Context, id, status string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE requirement SET status = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, status, id)
	return err
}

// SetApplication 把需求归属到某应用（发布自动部署后回填 application_id）。
func (r *Repository) SetApplication(ctx context.Context, id, appID string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE requirement SET application_id = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, appID, id)
	return err
}
