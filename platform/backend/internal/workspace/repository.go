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

// Overview 空间概览：空间元信息 + 各资源计数（成员/应用/需求/变更/发布/已部署应用）。
func (r *Repository) Overview(ctx context.Context, psID string) (*Overview, error) {
	ps, err := r.GetProjectSpace(ctx, psID)
	if err != nil {
		return nil, err
	}
	o := &Overview{Space: *ps}
	cnt := func(q string) int {
		var n int
		_ = r.db.GetContext(ctx, &n, q, psID)
		return n
	}
	o.Members = cnt(`SELECT COUNT(*) FROM membership WHERE project_space_id=?`)
	o.Apps = cnt(`SELECT COUNT(*) FROM appdeploy_application WHERE project_space_id=?`)
	o.DeployedApps = cnt(`SELECT COUNT(*) FROM appdeploy_application WHERE project_space_id=? AND status='running'`)
	o.Requirements = cnt(`SELECT COUNT(*) FROM requirement WHERE project_space_id=?`)
	o.Changes = cnt(`SELECT COUNT(*) FROM change_request WHERE project_space_id=?`)
	o.Releases = cnt(`SELECT COUNT(*) FROM release_record WHERE project_space_id=?`)
	return o, nil
}

// Overview 空间概览。
type Overview struct {
	Space        ProjectSpace `json:"space"`
	Members      int          `json:"members"`
	Apps         int          `json:"apps"`
	DeployedApps int          `json:"deployed_apps"`
	Requirements int          `json:"requirements"`
	Changes      int          `json:"changes"`
	Releases     int          `json:"releases"`
}
