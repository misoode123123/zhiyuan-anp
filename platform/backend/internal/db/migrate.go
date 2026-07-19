// Package db 负责数据库连接与迁移。
//
// 迁移：embed SQL 文件（migrations/pg/*.sql），按文件名版本排序执行，
// schema_migrations 表记录已应用版本，幂等。仅 PostgreSQL（pgx）。
// SQLite 驱动在 db.go 保留（Open 支持），但 Migrate 不支持 SQLite（切 PG 后用 PG）。
package db

import (
	"context"
	"embed"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

//go:embed migrations/pg/*.sql
var migrationFS embed.FS

// Migrate 执行 embed 的 PG 迁移文件（按 *.up.sql 文件名版本排序，幂等）。
// 已在 schema_migrations 记录的跳过；未记录的执行并记录。
func Migrate(ctx context.Context, db *sqlx.DB) error {
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    TEXT PRIMARY KEY,
		applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	entries, err := migrationFS.ReadDir("migrations/pg")
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}
	var ups []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".up.sql") {
			ups = append(ups, e.Name())
		}
	}
	sort.Strings(ups)

	for _, name := range ups {
		version := strings.TrimSuffix(name, ".up.sql")
		var applied bool
		if err := db.GetContext(ctx, &applied,
			`SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = $1)`, version); err != nil {
			return fmt.Errorf("check migration %s: %w", version, err)
		}
		if applied {
			continue
		}
		sqlBytes, err := migrationFS.ReadFile("migrations/pg/" + name)
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}
		if _, err := db.ExecContext(ctx, string(sqlBytes)); err != nil {
			return fmt.Errorf("exec migration %s: %w", name, err)
		}
		if _, err := db.ExecContext(ctx,
			`INSERT INTO schema_migrations(version) VALUES ($1)`, version); err != nil {
			return fmt.Errorf("record migration %s: %w", version, err)
		}
	}
	return nil
}

// SeedUsers 若 user 表为空，播种演示用户（admin/dev1/biz1，与 SeedBootstrapMembers 成员名对齐）。
func SeedUsers(ctx context.Context, db *sqlx.DB) error {
	var n int
	if err := db.GetContext(ctx, &n, `SELECT COUNT(*) FROM "user"`); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	demos := []struct{ name, email string }{
		{"admin", "admin@anp.local"},
		{"dev1", "dev1@anp.local"},
		{"biz1", "biz1@anp.local"},
	}
	for _, u := range demos {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO "user" (id, name, email, status) VALUES ($1, $2, $3, 'active')`,
			"usr_"+uuid.NewString()[:20], u.name, u.email); err != nil {
			return err
		}
	}
	return nil
}

// SeedBootstrapMembers 确保「默认项目空间 + 一组演示成员」存在（幂等，ON CONFLICT DO NOTHING）。
//
//	admin  —— 管理员，全权（默认登录用户）
//	dev1   —— 研发，可派编码/审批，不可改配置
//	biz1   —— 业务，可提需求，不可派编码/改配置
func SeedBootstrapMembers(ctx context.Context, db *sqlx.DB) error {
	if _, err := db.ExecContext(ctx,
		`INSERT INTO project_space (id, name, slug, status) VALUES ($1, $2, $3, 'active')
		 ON CONFLICT (id) DO NOTHING`,
		"ps_default", "默认空间", "default"); err != nil {
		return err
	}
	demo := []struct{ user, role string }{
		{"admin", "admin"},
		{"dev1", "dev"},
		{"biz1", "business"},
	}
	for _, m := range demo {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO membership (id, project_space_id, user_id, role) VALUES ($1, $2, $3, $4)
			 ON CONFLICT DO NOTHING`,
			"mbr_"+uuid.NewString()[:20], "ps_default", m.user, m.role); err != nil {
			return err
		}
	}
	return nil
}

// SeedDemoSkills 若默认空间 capability_skill 为空，播种两条 active demo 技能。
func SeedDemoSkills(ctx context.Context, db *sqlx.DB) error {
	var n int
	if err := db.GetContext(ctx, &n, `SELECT COUNT(*) FROM capability_skill WHERE project_space_id = $1`, "ps_default"); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	demos := []struct {
		code, name, category, prompt string
	}{
		{"data-qa", "数据问答", "data_qa", "你是数据问答技能。根据用户输入的业务问题，结合上下文给出简洁准确的分析与数据结论。\n\n用户输入：{input}"},
		{"doc-gen", "文档生成", "doc_gen", "你是文档生成技能。根据用户输入，生成结构清晰、专业规范的文档（Markdown）。\n\n主题/要求：{input}"},
	}
	for _, d := range demos {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO capability_skill (id, project_space_id, code, name, description, category, prompt_template, version, status, risk_level, is_public)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, '1.0.0', 'active', 'low', TRUE)`,
			"skl_"+uuid.NewString()[:20], "ps_default", d.code, d.name, d.name+" 技能", d.category, d.prompt); err != nil {
			return err
		}
	}
	return nil
}

// SeedDemoSOPs 若默认空间 ops_sop 为空，播种两条示例运维预案。
func SeedDemoSOPs(ctx context.Context, db *sqlx.DB) error {
	var n int
	if err := db.GetContext(ctx, &n, `SELECT COUNT(*) FROM ops_sop WHERE project_space_id = $1`, "ps_default"); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	demos := []struct {
		code, name, category, risk, steps, rollback string
		approval                                    bool
	}{
		{
			code: "RESTART-POD", name: "Pod 重启（CrashLoop）", category: "restart", risk: "low",
			steps:    "1. 定位异常 Pod（kubectl get pods）；2. kubectl delete pod <name>；3. 观察新 Pod 启动日志；4. 确认服务恢复。",
			rollback: "若重启后仍 CrashLoop，回滚至上一个稳定镜像版本。",
		},
		{
			code: "SCALE-OUT", name: "服务扩容（流量突增）", category: "scale", risk: "medium",
			steps:    "1. 确认负载指标（CPU/QPS）；2. kubectl scale deploy/<name> --replicas=N；3. 观察 HPA 与延迟；4. 确认扩容生效。",
			rollback: "流量回落后 kubectl scale 回原副本数。",
			approval: true,
		},
	}
	for _, d := range demos {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO ops_sop (id, project_space_id, code, name, category, risk_level, steps, rollback, requires_approval, status)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'active')`,
			"sop_"+uuid.NewString()[:20], "ps_default", d.code, d.name, d.category, d.risk, d.steps, d.rollback, d.approval); err != nil {
			return err
		}
	}
	return nil
}

// SeedDemoStandards 若 coding_standard 为空，播种两条全局 demo 规范（呼应平台五约束）。
func SeedDemoStandards(ctx context.Context, db *sqlx.DB) error {
	var n int
	if err := db.GetContext(ctx, &n, `SELECT COUNT(*) FROM coding_standard`); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	demos := []struct{ name, category, content string }{
		{"产出五约束", "general", "AI 产出须满足：可校验、可追溯、可回滚、守边界、守权限"},
		{"安全基线", "security", "密钥与敏感信息不得硬编码；外部输入必须校验；不得在日志/响应中暴露凭据"},
	}
	for _, d := range demos {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO coding_standard (id, project_space_id, name, category, content, priority, enabled)
			 VALUES ($1, NULL, $2, $3, $4, 100, TRUE)`,
			"std_"+uuid.NewString()[:21], d.name, d.category, d.content); err != nil {
			return err
		}
	}
	return nil
}
