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

const cols = `id, project_space_id, name, category, content, priority, enabled, scope, module, created_at, updated_at`

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
		`SELECT `+cols+` FROM coding_standard WHERE project_space_id=$1 ORDER BY priority, created_at`, psID)
	return list, err
}

// ListEffective 生效规范 = 全局 + 指定项目空间，enabled=1，按 priority 升序。
// 保留：dev.NewCodingAgent 注入 prompt 用（旧逻辑兼容，不依赖 scope/module）。
func (s *Store) ListEffective(ctx context.Context, psID string) ([]Standard, error) {
	var list []Standard
	err := s.db.SelectContext(ctx, &list,
		`SELECT `+cols+` FROM coding_standard
		 WHERE enabled AND (project_space_id IS NULL OR project_space_id=$1)
		 ORDER BY priority, created_at`, psID)
	return list, err
}

// ListByScope 按层级列规范（enabled 优先，但不强制过滤——上层 UI 看全部，禁用项灰显）。
// scope 取 platform/app/module；module 仅 scope=module 时作为子过滤（"" 即该层全部）。
// scope="" 时全量返回（按 scope 优先级 + priority）。
func (s *Store) ListByScope(ctx context.Context, scope, module string) ([]Standard, error) {
	var list []Standard
	// scope 优先级：platform(0) > app(1) > module(2)
	order := `CASE scope WHEN 'platform' THEN 0 WHEN 'app' THEN 1 ELSE 2 END, priority, created_at`
	switch {
	case scope == "":
		err := s.db.SelectContext(ctx, &list,
			`SELECT `+cols+` FROM coding_standard ORDER BY `+order)
		return list, err
	case scope == ScopeModule && module != "":
		err := s.db.SelectContext(ctx, &list,
			`SELECT `+cols+` FROM coding_standard WHERE scope=$1 AND module=$2 ORDER BY `+order,
			scope, module)
		return list, err
	default:
		err := s.db.SelectContext(ctx, &list,
			`SELECT `+cols+` FROM coding_standard WHERE scope=$1 ORDER BY `+order, scope)
		return list, err
	}
}

// Aggregate 聚合规范 = 平台级(全) + 应用级(全) + 指定模块级(module 子过滤)。
// enabled=1 优先（暂返回所有，由 UI/导出决定是否过滤禁用项——当前包含禁用项以便审计）。
// 排序：scope 优先级(platform>app>module) → priority → created_at。
// module="" 时仅返回 platform + app（不附模块级）。
func (s *Store) Aggregate(ctx context.Context, module string) ([]Standard, error) {
	var list []Standard
	order := `CASE scope WHEN 'platform' THEN 0 WHEN 'app' THEN 1 ELSE 2 END, priority, created_at`
	if module == "" {
		err := s.db.SelectContext(ctx, &list,
			`SELECT `+cols+` FROM coding_standard
			 WHERE scope IN ('platform','app')
			 ORDER BY `+order)
		return list, err
	}
	err := s.db.SelectContext(ctx, &list,
		`SELECT `+cols+` FROM coding_standard
		 WHERE (scope IN ('platform','app'))
            OR (scope='module' AND module=$1)
		 ORDER BY `+order, module)
	return list, err
}

// Get 取单条。
func (s *Store) Get(ctx context.Context, id string) (*Standard, error) {
	var st Standard
	err := s.db.GetContext(ctx, &st, `SELECT `+cols+` FROM coding_standard WHERE id=$1`, id)
	return &st, err
}

// Create 新建（id 自动生成；ProjectSpaceID 为 nil 即全局；Scope/Module 默认 platform/""）。
func (s *Store) Create(ctx context.Context, st *Standard) error {
	if st.Scope == "" {
		st.Scope = ScopePlatform
	}
	if st.Scope != ScopeModule {
		st.Module = "" // 仅 module 层有 module 子字段
	}
	st.ID = "std_" + uuid.NewString()[:21]
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO coding_standard (id, project_space_id, name, category, content, priority, enabled, scope, module)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		st.ID, st.ProjectSpaceID, st.Name, st.Category, st.Content, st.Priority, st.Enabled, st.Scope, st.Module)
	return err
}

// Update 更新（不含 project_space_id/scope，层级不可改）。
func (s *Store) Update(ctx context.Context, st *Standard) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE coding_standard SET name=$1, category=$2, content=$3, priority=$4, enabled=$5, module=$6, updated_at=CURRENT_TIMESTAMP
		 WHERE id=$7`, st.Name, st.Category, st.Content, st.Priority, st.Enabled, st.Module, st.ID)
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
	_, err := s.db.ExecContext(ctx, `UPDATE coding_standard SET enabled=$1, updated_at=CURRENT_TIMESTAMP WHERE id=$2`, enabled, id)
	return err
}

// Delete 删除。
func (s *Store) Delete(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM coding_standard WHERE id=$1`, id)
	return err
}
