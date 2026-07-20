package release

import (
	"context"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// Store 发布记录数据访问。
type Store struct {
	db *sqlx.DB
}

// NewStore 构造 Store。
func NewStore(db *sqlx.DB) *Store { return &Store{db: db} }

// Create 新建发布记录。
func (s *Store) Create(ctx context.Context, r *Release) error {
	r.ID = "rel_" + uuid.NewString()[:20]
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO release_record (id, project_space_id, change_id, version, status)
		 VALUES ($1, $2, $3, $4, $5)`,
		r.ID, r.ProjectSpaceID, r.ChangeID, r.Version, r.Status)
	return err
}

// List 列出项目空间的发布记录。
func (s *Store) List(ctx context.Context, projectSpaceID string) ([]Release, error) {
	var list []Release
	err := s.db.SelectContext(ctx, &list,
		`SELECT r.id, r.project_space_id, r.change_id, r.version, r.status, r.created_at,
		        COALESCE(a.name,'') AS app_name,
		        COALESCE(ch.reviewer,'') AS reviewer,
		        COALESCE(ch.prompt,'') AS prompt,
		        COALESCE(ch.output,'') AS output
		 FROM release_record r
		 LEFT JOIN change_request ch ON ch.id = r.change_id
		 LEFT JOIN appdeploy_application a
		   ON a.id = ch.source_id
		   OR a.id IN (SELECT application_id FROM requirement WHERE id = ch.source_id)
		 WHERE r.project_space_id = $1 ORDER BY r.created_at DESC`, projectSpaceID)
	return list, err
}

// Count 项目空间的发布数（用于版本号自增）。
func (s *Store) Count(ctx context.Context, projectSpaceID string) (int, error) {
	var n int
	err := s.db.GetContext(ctx, &n, `SELECT COUNT(*) FROM release_record WHERE project_space_id = $1`, projectSpaceID)
	return n, err
}
