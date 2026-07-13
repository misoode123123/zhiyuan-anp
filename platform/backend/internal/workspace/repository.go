package workspace

import (
	"context"
	"database/sql"
	"errors"

	"github.com/jmoiron/sqlx"
)

// ErrNotFound 未找到记录。
var ErrNotFound = errors.New("not found")

// Repository 项目空间数据访问。
type Repository struct {
	db *sqlx.DB
}

// NewRepository 构造 Repository。
func NewRepository(db *sqlx.DB) *Repository { return &Repository{db: db} }

func (r *Repository) CreateProjectSpace(ctx context.Context, ps *ProjectSpace) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO project_space (id, name, slug, status) VALUES (?, ?, ?, ?)`,
		ps.ID, ps.Name, ps.Slug, ps.Status)
	return err
}

func (r *Repository) GetProjectSpace(ctx context.Context, id string) (*ProjectSpace, error) {
	var ps ProjectSpace
	err := r.db.GetContext(ctx, &ps, `SELECT * FROM project_space WHERE id = ?`, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &ps, err
}

func (r *Repository) ListProjectSpaces(ctx context.Context) ([]ProjectSpace, error) {
	var list []ProjectSpace
	err := r.db.SelectContext(ctx, &list,
		`SELECT * FROM project_space ORDER BY created_at DESC`)
	return list, err
}

func (r *Repository) CreateProject(ctx context.Context, p *Project) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO project (id, project_space_id, name, slug, status) VALUES (?, ?, ?, ?, ?)`,
		p.ID, p.ProjectSpaceID, p.Name, p.Slug, p.Status)
	return err
}

func (r *Repository) ListProjects(ctx context.Context, projectSpaceID string) ([]Project, error) {
	var list []Project
	err := r.db.SelectContext(ctx, &list,
		`SELECT * FROM project WHERE project_space_id = ? ORDER BY created_at DESC`,
		projectSpaceID)
	return list, err
}
