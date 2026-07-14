package standard

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// Store 编码规范数据访问。
type Store struct {
	db *sqlx.DB
}

// NewStore 构造 Store。
func NewStore(db *sqlx.DB) *Store { return &Store{db: db} }

const cols = `id, project_space_id, name, category, content, priority, enabled, created_at, updated_at`

// ListGlobal 全局规范（project_space_id IS NULL）。
func (s *Store) ListGlobal(ctx context.Context) ([]Standard, error) {
	var list []Standard
	err := s.db.SelectContext(ctx, &list,
		`SELECT `+cols+` FROM coding_standard WHERE project_space_id IS NULL ORDER BY priority, created_at`)
	return list, err
}

// ListByProjectSpace 某项目空间的项目级规范。
func (s *Store) ListByProjectSpace(ctx context.Context, psID string) ([]Standard, error) {
	var list []Standard
	err := s.db.SelectContext(ctx, &list,
		`SELECT `+cols+` FROM coding_standard WHERE project_space_id=? ORDER BY priority, created_at`, psID)
	return list, err
}

// ListEffective 生效规范 = 全局 + 指定项目空间，enabled=1，按 priority 升序。
func (s *Store) ListEffective(ctx context.Context, psID string) ([]Standard, error) {
	var list []Standard
	err := s.db.SelectContext(ctx, &list,
		`SELECT `+cols+` FROM coding_standard
		 WHERE enabled=1 AND (project_space_id IS NULL OR project_space_id=?)
		 ORDER BY priority, created_at`, psID)
	return list, err
}

// Get 取单条。
func (s *Store) Get(ctx context.Context, id string) (*Standard, error) {
	var st Standard
	err := s.db.GetContext(ctx, &st, `SELECT `+cols+` FROM coding_standard WHERE id=?`, id)
	return &st, err
}

// Create 新建（id 自动生成；ProjectSpaceID 为 nil 即全局）。
func (s *Store) Create(ctx context.Context, st *Standard) error {
	st.ID = "std_" + uuid.NewString()[:21]
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO coding_standard (id, project_space_id, name, category, content, priority, enabled)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		st.ID, st.ProjectSpaceID, st.Name, st.Category, st.Content, st.Priority, st.Enabled)
	return err
}

// Update 更新（不含 project_space_id，层级不可改）。
func (s *Store) Update(ctx context.Context, st *Standard) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE coding_standard SET name=?, category=?, content=?, priority=?, enabled=?, updated_at=CURRENT_TIMESTAMP
		 WHERE id=?`, st.Name, st.Category, st.Content, st.Priority, st.Enabled, st.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("编码规范 %s 不存在", st.ID)
	}
	return nil
}

// SetEnabled 启用/禁用。
func (s *Store) SetEnabled(ctx context.Context, id string, enabled bool) error {
	_, err := s.db.ExecContext(ctx, `UPDATE coding_standard SET enabled=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`, enabled, id)
	return err
}

// Delete 删除。
func (s *Store) Delete(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM coding_standard WHERE id=?`, id)
	return err
}
