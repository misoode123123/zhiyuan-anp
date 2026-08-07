// Package main —— genseedstandards 从 internal/db.StandardSeeds 生成幂等升级 SQL。
//
// 用途：.28 等已运行环境通过此 SQL 把全局规范（project_space_id IS NULL）补齐到最新。
// 单一事实源：internal/db/standards_seed.go（Go seed 与本 SQL 共用，避免内容漂移）。
//
// 重新生成：
//
//	cd platform/backend && go run ./cmd/genseedstandards > scripts/standards_seed.sql
//
// 生成产物幂等：DELETE 全局规范 + INSERT 全套；项目级规范（project_space_id 非空）不动。
package main

import (
	"fmt"
	"strings"

	"zhiyuan-anp/platform/backend/internal/db"
)

// sqlID 生成确定性、可读的 id（基于 scope/module + 序号），便于 git diff 稳定 + 审计。
// 序号在该 (scope, module) 内从 1 起。
func sqlID(scope, module string, idx int) string {
	parts := []string{"std"}
	parts = append(parts, scope)
	if module != "" {
		parts = append(parts, module)
	}
	parts = append(parts, fmt.Sprintf("%d", idx))
	return strings.Join(parts, "_")
}

// sqlLit 把字符串转义为 PG 单引号字面量（' → ”）。
func sqlLit(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func main() {
	var b strings.Builder
	b.WriteString("-- standards_seed.sql\n")
	b.WriteString("-- 全局编码规范（coding_standard, project_space_id IS NULL）幂等升级 SQL。\n")
	b.WriteString("--\n")
	b.WriteString("-- 数据源：internal/db/standards_seed.go（StandardSeeds）。\n")
	b.WriteString("-- 重新生成：cd platform/backend && go run ./cmd/genseedstandards > scripts/standards_seed.sql\n")
	b.WriteString("--\n")
	b.WriteString("-- 执行方式（.28 等已运行环境）：\n")
	b.WriteString("--   psql \"$DATABASE_URL\" -f scripts/standards_seed.sql\n")
	b.WriteString("--\n")
	b.WriteString("-- 幂等说明：DELETE 全局规范（含旧的 5 条占位）后 INSERT 最新全套。\n")
	b.WriteString("-- 项目级规范（project_space_id 非空）不动，保留用户在 /governance 的调整。\n")
	b.WriteString("-- 新环境（coding_standard 为空）无需跑此 SQL——main.go 启动时 SeedDemoStandards 自动播种。\n\n")

	b.WriteString("-- 1. 清空全局规范（保留项目级）\n")
	b.WriteString("DELETE FROM coding_standard WHERE project_space_id IS NULL;\n\n")

	b.WriteString("-- 2. 插入全套规范（platform 6 / app 5 / module: api 6 + db 5 + code 5 + form 3 + ui 4 = 34 条）\n")
	b.WriteString("INSERT INTO coding_standard (id, project_space_id, name, category, content, priority, enabled, scope, module) VALUES\n")

	// 按 (scope, module) 分组计数，生成确定性 id。
	counter := map[string]int{}
	n := len(db.StandardSeeds)
	for i, s := range db.StandardSeeds {
		key := s.Scope + "|" + s.Module
		counter[key]++
		id := sqlID(s.Scope, s.Module, counter[key])

		modLit := "''"
		if s.Module != "" {
			modLit = sqlLit(s.Module)
		}
		fmt.Fprintf(&b, "  (%s, NULL, %s, %s, %s, %d, TRUE, %s, %s)",
			sqlLit(id), sqlLit(s.Name), sqlLit(s.Category), sqlLit(s.Content),
			s.Priority, sqlLit(s.Scope), modLit)
		if i == n-1 {
			b.WriteString(";\n")
		} else {
			b.WriteString(",\n")
		}
	}

	b.WriteString("\n-- 3. 校验：各层条数\n")
	b.WriteString("-- SELECT scope, COALESCE(NULLIF(module,''),'(none)') AS module, COUNT(*)\n")
	b.WriteString("--   FROM coding_standard WHERE project_space_id IS NULL\n")
	b.WriteString("--   GROUP BY scope, module ORDER BY scope, module;\n")

	fmt.Print(b.String())
}
