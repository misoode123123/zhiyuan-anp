package db

import (
	"context"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// sqliteSchema 是 M0 的初始 schema（项目空间、项目、成员）。
// 切 PostgreSQL 后改为 golang-migrate 管理 SQL 文件。
const sqliteSchema = `
CREATE TABLE IF NOT EXISTS project_space (
  id         TEXT PRIMARY KEY,
  name       TEXT NOT NULL,
  slug       TEXT NOT NULL UNIQUE,
  status     TEXT NOT NULL DEFAULT 'active',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS project (
  id               TEXT PRIMARY KEY,
  project_space_id TEXT NOT NULL REFERENCES project_space(id),
  name             TEXT NOT NULL,
  slug             TEXT NOT NULL,
  status           TEXT NOT NULL DEFAULT 'active',
  created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE (project_space_id, slug)
);
CREATE INDEX IF NOT EXISTS idx_project_space ON project(project_space_id);

CREATE TABLE IF NOT EXISTS membership (
  id               TEXT PRIMARY KEY,
  project_space_id TEXT NOT NULL REFERENCES project_space(id),
  user_id          TEXT NOT NULL,
  role             TEXT NOT NULL,
  created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE (project_space_id, user_id)
);

CREATE TABLE IF NOT EXISTS requirement (
  id                  TEXT PRIMARY KEY,
  project_space_id    TEXT NOT NULL,
  title               TEXT NOT NULL,
  description         TEXT,
  user_story          TEXT,
  acceptance_criteria TEXT,
  status              TEXT NOT NULL DEFAULT 'draft',
  created_at          DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at          DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_requirement_ps ON requirement(project_space_id);

CREATE TABLE IF NOT EXISTS system_config (
  key         TEXT PRIMARY KEY,
  value       TEXT,
  category    TEXT NOT NULL DEFAULT 'general',
  description TEXT,
  updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS rule (
  id              TEXT PRIMARY KEY,
  name            TEXT NOT NULL,
  category        TEXT NOT NULL DEFAULT 'general',
  type            TEXT NOT NULL DEFAULT 'mandatory',   -- mandatory(强制)/should(应遵循)/reference(参考)
  condition       TEXT NOT NULL,                       -- 正则或关键字
  condition_field TEXT NOT NULL DEFAULT 'prompt',      -- prompt/output/code_path
  action          TEXT NOT NULL DEFAULT 'block',       -- block/warn/require_approval
  scope           TEXT NOT NULL DEFAULT 'all',         -- dev/requirement/all
  enabled         INTEGER NOT NULL DEFAULT 1,
  description     TEXT,
  created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS change_request (
  id               TEXT PRIMARY KEY,
  project_space_id TEXT,
  kind             TEXT NOT NULL DEFAULT 'code',   -- code / dispatch
  source_id        TEXT,                           -- requirement_id 或空
  repo_dir         TEXT,
  prompt           TEXT,
  model            TEXT,
  output           TEXT,
  status           TEXT NOT NULL DEFAULT 'pending', -- pending / approved / rejected
  reviewer         TEXT,
  reviewed_at      DATETIME,
  created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS test_case (
  id               TEXT PRIMARY KEY,
  project_space_id TEXT NOT NULL,
  requirement_id   TEXT,
  title            TEXT NOT NULL,
  steps            TEXT,        -- JSON 数组
  expected         TEXT,
  status           TEXT NOT NULL DEFAULT 'draft',
  created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_test_case_ps ON test_case(project_space_id);

CREATE TABLE IF NOT EXISTS release_record (
  id               TEXT PRIMARY KEY,
  project_space_id TEXT NOT NULL,
  change_id        TEXT,
  version          TEXT NOT NULL,
  status           TEXT NOT NULL DEFAULT 'released',
  created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS usage_record (
  id               TEXT PRIMARY KEY,
  project_space_id TEXT NOT NULL,
  model            TEXT,
  kind             TEXT,                 -- chat / spec / test / code
  prompt_tokens    INTEGER NOT NULL DEFAULT 0,
  completion_tokens INTEGER NOT NULL DEFAULT 0,
  total_tokens     INTEGER NOT NULL DEFAULT 0,
  created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_usage_ps ON usage_record(project_space_id);

CREATE TABLE IF NOT EXISTS code_task (
  id               TEXT PRIMARY KEY,
  project_space_id TEXT,
  kind             TEXT,
  source_id        TEXT,
  repo_dir         TEXT,
  prompt           TEXT,
  model            TEXT,
  status           TEXT NOT NULL DEFAULT 'running',   -- running/completed/failed
  output           TEXT,
  change_id        TEXT,
  created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS coding_standard (
  id               TEXT PRIMARY KEY,
  project_space_id TEXT NULL,
  name             TEXT NOT NULL,
  category         TEXT NOT NULL DEFAULT 'general',
  content          TEXT NOT NULL,
  priority         INTEGER NOT NULL DEFAULT 100,
  enabled          INTEGER NOT NULL DEFAULT 1,
  created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_coding_standard_ps ON coding_standard(project_space_id);

CREATE TABLE IF NOT EXISTS conversation (
  id               TEXT PRIMARY KEY,
  project_space_id TEXT NOT NULL REFERENCES project_space(id),
  status           TEXT NOT NULL DEFAULT 'active',
  title            TEXT,
  requirement_id   TEXT,
  created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_conversation_ps ON conversation(project_space_id);

CREATE TABLE IF NOT EXISTS message (
  id              TEXT PRIMARY KEY,
  conversation_id TEXT NOT NULL REFERENCES conversation(id),
  role            TEXT NOT NULL,
  content         TEXT NOT NULL,
  media_kind      TEXT NOT NULL DEFAULT 'text',
  created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_message_conv ON message(conversation_id);

CREATE TABLE IF NOT EXISTS ops_alert (
  id               TEXT PRIMARY KEY,
  project_space_id TEXT NOT NULL,
  source           TEXT NOT NULL DEFAULT 'custom',   -- patrol/prometheus/loki/k8s/custom
  severity         TEXT NOT NULL DEFAULT 'warning',   -- critical/warning/info
  status           TEXT NOT NULL DEFAULT 'firing',    -- firing/resolved/suppressed
  fingerprint      TEXT NOT NULL,                     -- 去重指纹
  title            TEXT NOT NULL,
  description      TEXT,
  fired_at         DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  resolved_at      DATETIME,
  created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_ops_alert_ps ON ops_alert(project_space_id, status);
CREATE INDEX IF NOT EXISTS idx_ops_alert_fp ON ops_alert(fingerprint);

CREATE TABLE IF NOT EXISTS ops_sop (
  id                TEXT PRIMARY KEY,
  project_space_id  TEXT NOT NULL,
  code              TEXT NOT NULL,
  name              TEXT NOT NULL,
  description       TEXT,
  category          TEXT NOT NULL DEFAULT 'restart',  -- restart/scale/cache/traffic/data
  risk_level        TEXT NOT NULL DEFAULT 'low',       -- low/medium/high
  steps             TEXT,
  rollback          TEXT,
  requires_approval INTEGER NOT NULL DEFAULT 0,
  status            TEXT NOT NULL DEFAULT 'draft',     -- draft/active/deprecated
  created_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE (project_space_id, code)
);
CREATE INDEX IF NOT EXISTS idx_ops_sop_ps ON ops_sop(project_space_id, status);
`

// Migrate 执行启动期 schema 初始化（幂等）。
func Migrate(ctx context.Context, db *sqlx.DB) error {
	_, err := db.ExecContext(ctx, sqliteSchema)
	return err
}

// SeedBootstrapMembers 确保「默认项目空间 + 一组演示成员」存在。
// RBAC 强制接入后，所有写/危险操作需鉴权；此种子保证系统首次可用并支持多角色演示：
//
//	admin  —— 管理员，全权（默认登录用户）
//	dev1   —— 研发，可派编码/审批，不可改配置
//	biz1   —— 业务，可提需求，不可派编码/改配置
//
// 幂等：project_space 主键、membership (project_space_id,user_id) 唯一约束兜底重复。
func SeedBootstrapMembers(ctx context.Context, db *sqlx.DB) error {
	if _, err := db.ExecContext(ctx,
		`INSERT OR IGNORE INTO project_space (id, name, slug, status) VALUES ('ps_default', '默认空间', 'default', 'active')`); err != nil {
		return err
	}
	demo := []struct{ user, role string }{
		{"admin", "admin"},
		{"dev1", "dev"},
		{"biz1", "business"},
	}
	for _, m := range demo {
		if _, err := db.ExecContext(ctx,
			`INSERT OR IGNORE INTO membership (id, project_space_id, user_id, role) VALUES (?, 'ps_default', ?, ?)`,
			"mbr_"+uuid.NewString()[:20], m.user, m.role); err != nil {
			return err
		}
	}
	return nil
}

// SeedDemoSOPs 若默认空间的 ops_sop 表为空，播种两条示例运维预案。
func SeedDemoSOPs(ctx context.Context, db *sqlx.DB) error {
	var n int
	if err := db.GetContext(ctx, &n, `SELECT COUNT(*) FROM ops_sop WHERE project_space_id='ps_default'`); err != nil {
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
		apv := 0
		if d.approval {
			apv = 1
		}
		if _, err := db.ExecContext(ctx,
			`INSERT INTO ops_sop (id, project_space_id, code, name, category, risk_level, steps, rollback, requires_approval, status)
			 VALUES (?, 'ps_default', ?, ?, ?, ?, ?, ?, ?, 'active')`,
			"sop_"+uuid.NewString()[:20], d.code, d.name, d.category, d.risk, d.steps, d.rollback, apv); err != nil {
			return err
		}
	}
	return nil
}

// SeedDemoStandards 若 coding_standard 表为空，播种两条全局 demo 规范（呼应平台五约束）。
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
			 VALUES (?, NULL, ?, ?, ?, 100, 1)`,
			"std_"+uuid.NewString()[:21], d.name, d.category, d.content); err != nil {
			return err
		}
	}
	return nil
}
