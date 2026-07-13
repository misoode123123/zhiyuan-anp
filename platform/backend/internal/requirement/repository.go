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

func (r *Repository) Create(ctx context.Context, req *Requirement) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO requirement (id, project_space_id, title, description, user_story, acceptance_criteria, status)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		req.ID, req.ProjectSpaceID, req.Title, req.Description, req.UserStory, req.AcceptanceCriteria, req.Status)
	return err
}

func (r *Repository) List(ctx context.Context, projectSpaceID string) ([]Requirement, error) {
	var list []Requirement
	err := r.db.SelectContext(ctx, &list,
		`SELECT * FROM requirement WHERE project_space_id = ? ORDER BY created_at DESC`, projectSpaceID)
	return list, err
}

func (r *Repository) Get(ctx context.Context, id string) (*Requirement, error) {
	var req Requirement
	err := r.db.GetContext(ctx, &req, `SELECT * FROM requirement WHERE id = ?`, id)
	return &req, err
}

// UpdateStatus 更新需求状态（发布后→delivered，闭环需求生命周期）。
func (r *Repository) UpdateStatus(ctx context.Context, id, status string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE requirement SET status = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, status, id)
	return err
}
