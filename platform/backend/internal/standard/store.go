package standard

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

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
// module 行为：空串=保留原值（前端 PUT 不传 module 时 updateBody.Module 为空串，
// 避免误把 module 层规范 module 字段清空）；非空=改子模块。
// scope 不可改 + scope!=module 时 module 本就为空 → 不存在「清空 module 降级」诉求。
func (s *Store) Update(ctx context.Context, st *Standard) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE coding_standard SET name=$1, category=$2, content=$3, priority=$4, enabled=$5,
		 module = COALESCE(NULLIF($6,''), module),
		 updated_at=CURRENT_TIMESTAMP
		 WHERE id=$7`, st.Name, st.Category, st.Content, st.Priority, st.Enabled, st.Module, st.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("编码规范 %s 不存在", st.ID)
	}
	// 回查：把 st 替换为 DB 当前真实值。module 保留逻辑下，调用方传入的 st.Module 可能是空串
	//（前端不传 module 时），回查确保返回响应/上层用到的字段（module/scope/created_at 等）准确。
	if cur, gerr := s.Get(ctx, st.ID); gerr == nil {
		*st = *cur
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

// allModules 全 module 子字段（RefreshAgentsMD module="" 时遍历聚合，生成最全规范）。
var allModules = []string{ModuleAPI, ModuleForm, ModuleDB, ModuleCode, ModuleUI}

// AggregateFor 按 psID 过滤的聚合（RefreshAgentsMD 用，dev/opencode 生成应用 AGENTS.md）。
//   - psID="" 仅全局规范（project_space_id IS NULL）；
//     psID 非空 → 全局 + 该项目空间的规范。
//   - module 非空 → platform + app + 指定 module；
//     module="" → platform + app + 全部 module（api/form/db/code/ui），生成最完整的开发规范。
//
// 与 Aggregate 区别：Aggregate 不带 psID（全局管理员预览用，看到全部）；
// AggregateFor 按 psID 过滤项目级，避免应用 AGENTS.md 混入其他项目空间的规范。
// 排序：scope 优先级(platform>app>module) → priority → created_at。
func (s *Store) AggregateFor(ctx context.Context, psID, module string) ([]Standard, error) {
	// platform + app（一次查询）
	pa, err := s.aggregateLayer(ctx, psID, "")
	if err != nil {
		return nil, err
	}
	out := make([]Standard, 0, len(pa))
	out = append(out, pa...)
	// module 级：指定 module 单查；module="" 遍历全 module（每模块单查，避免 platform/app 重复）
	mods := []string{module}
	if module == "" {
		mods = allModules
	}
	for _, m := range mods {
		items, err := s.aggregateLayer(ctx, psID, m)
		if err != nil {
			return nil, err
		}
		out = append(out, items...)
	}
	return out, nil
}

// aggregateLayer 按 psID + module 过滤的单层聚合。
//   - module="" → scope IN ('platform','app')（platform + app 一次查全）；
//   - module 非空 → scope='module' AND module=?（只该 module 层，不含 platform/app ——
//     AggregateFor 顶层已查 platform/app，此处避免重复）。
//   - psID="" → project_space_id IS NULL；非空 → IS NULL OR =psID。
func (s *Store) aggregateLayer(ctx context.Context, psID, module string) ([]Standard, error) {
	var list []Standard
	order := `CASE scope WHEN 'platform' THEN 0 WHEN 'app' THEN 1 ELSE 2 END, priority, created_at`

	psFilter := `project_space_id IS NULL`
	args := []any{}
	if psID != "" {
		psFilter = `(project_space_id IS NULL OR project_space_id=$1)`
		args = append(args, psID)
	}
	var scopeClause string
	if module == "" {
		scopeClause = `scope IN ('platform','app')`
	} else {
		nextN := len(args) + 1
		scopeClause = fmt.Sprintf(`scope='module' AND module=$%d`, nextN)
		args = append(args, module)
	}
	q := `SELECT ` + cols + ` FROM coding_standard WHERE ` + psFilter + ` AND ` + scopeClause + ` ORDER BY ` + order
	if err := s.db.SelectContext(ctx, &list, q, args...); err != nil {
		return nil, err
	}
	return list, nil
}

// RefreshAgentsMD 聚合规范（按 psID 过滤；module="" 时含全 module）→ 写进应用 repo 的 AGENTS.md。
// dev 编码前 + opencode 工作台启动前都调它，保证 AGENTS.md 是最新的聚合规范。
// repoDir 不存在则创建；写文件 0644。
func (s *Store) RefreshAgentsMD(ctx context.Context, repoDir, psID, module string) error {
	list, err := s.AggregateFor(ctx, psID, module)
	if err != nil {
		return fmt.Errorf("聚合规范失败: %w", err)
	}
	md := BuildAgentsMarkdown(list, module)
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		return fmt.Errorf("创建 repo 目录失败(%s): %w", repoDir, err)
	}
	path := filepath.Join(repoDir, "AGENTS.md")
	if err := os.WriteFile(path, []byte(md), 0o644); err != nil {
		return fmt.Errorf("写 AGENTS.md 失败(%s): %w", path, err)
	}
	return nil
}
