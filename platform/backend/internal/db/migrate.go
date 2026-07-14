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

CREATE TABLE IF NOT EXISTS security_scan_result (
  id               TEXT PRIMARY KEY,
  project_space_id TEXT NOT NULL,
  scan_type        TEXT NOT NULL,            -- secret/sast/prompt/full
  risk_level       TEXT NOT NULL DEFAULT 'clean', -- critical/high/medium/low/clean
  total_findings   INTEGER NOT NULL DEFAULT 0,
  critical_count   INTEGER NOT NULL DEFAULT 0,
  high_count       INTEGER NOT NULL DEFAULT 0,
  medium_count     INTEGER NOT NULL DEFAULT 0,
  low_count        INTEGER NOT NULL DEFAULT 0,
  content_preview  TEXT,
  created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_sec_scan_ps ON security_scan_result(project_space_id);

CREATE TABLE IF NOT EXISTS security_finding (
  id               TEXT PRIMARY KEY,
  project_space_id TEXT NOT NULL,
  scan_result_id   TEXT NOT NULL REFERENCES security_scan_result(id),
  category         TEXT NOT NULL,            -- secret/sast/prompt
  rule_id          TEXT NOT NULL,            -- RULE-SEC-xxx
  severity         TEXT NOT NULL,            -- critical/high/medium/low
  title            TEXT NOT NULL,
  description      TEXT,
  line_number      INTEGER,
  code_snippet     TEXT,
  remediation      TEXT,
  confidence       REAL NOT NULL DEFAULT 1.0,
  status           TEXT NOT NULL DEFAULT 'open', -- open/suppressed/fixed
  created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  suppressed_at    DATETIME
);
CREATE INDEX IF NOT EXISTS idx_sec_finding_ps ON security_finding(project_space_id, status, severity);

CREATE TABLE IF NOT EXISTS security_data_classification (
  id                TEXT PRIMARY KEY,
  project_space_id  TEXT NOT NULL,
  field_name        TEXT NOT NULL,
  table_ref         TEXT NOT NULL,
  sensitivity_level TEXT NOT NULL DEFAULT 'internal', -- public/internal/confidential/restricted
  data_type         TEXT NOT NULL DEFAULT 'pii',      -- pii/pci/phi/secret/ip/personal
  masking_strategy  TEXT NOT NULL DEFAULT 'mask',     -- mask/hash/replace/suppress/synthetic
  status            TEXT NOT NULL DEFAULT 'draft',    -- draft/confirmed
  created_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_sec_dc_ps ON security_data_classification(project_space_id);

CREATE TABLE IF NOT EXISTS security_audit (
  id               TEXT PRIMARY KEY,
  project_space_id TEXT NOT NULL,
  actor_type       TEXT NOT NULL,   -- agent/human/system
  actor_id         TEXT,
  action           TEXT NOT NULL,   -- scan/suppress/gate/leak_blocked
  resource_type    TEXT,
  detail           TEXT,
  policy_decision  TEXT,            -- allow/deny/mask
  created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_sec_audit_ps ON security_audit(project_space_id);

CREATE TABLE IF NOT EXISTS capability_skill (
  id                TEXT PRIMARY KEY,
  project_space_id  TEXT NOT NULL,
  code              TEXT NOT NULL,
  name              TEXT NOT NULL,
  description       TEXT,
  category          TEXT NOT NULL DEFAULT 'assistant',  -- requirement/doc_gen/data_qa/approval/report/code/assistant
  prompt_template   TEXT,                                -- 提示模板（{input} 占位）
  version           TEXT NOT NULL DEFAULT '0.1.0',
  status            TEXT NOT NULL DEFAULT 'draft',      -- draft/pending_review/active/offline
  risk_level        TEXT NOT NULL DEFAULT 'low',
  is_public         INTEGER NOT NULL DEFAULT 0,
  data_access_scope TEXT,
  created_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE (project_space_id, code)
);
CREATE INDEX IF NOT EXISTS idx_cap_skill_status ON capability_skill(status);

CREATE TABLE IF NOT EXISTS capability_api_key (
  id               TEXT PRIMARY KEY,
  project_space_id TEXT NOT NULL,
  app_name         TEXT NOT NULL,
  key_hash         TEXT NOT NULL,
  key_prefix       TEXT NOT NULL,
  allowed_skills   TEXT,
  scope            TEXT NOT NULL DEFAULT 'write',
  status           TEXT NOT NULL DEFAULT 'active',
  expires_at       DATETIME,
  created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_cap_key_hash ON capability_api_key(key_hash);
CREATE INDEX IF NOT EXISTS idx_cap_key_ps ON capability_api_key(project_space_id, status);

CREATE TABLE IF NOT EXISTS capability_usage (
  id               TEXT PRIMARY KEY,
  project_space_id TEXT NOT NULL,
  api_key_id       TEXT,
  caller_app       TEXT,
  skill_id         TEXT,
  input_tokens     INTEGER NOT NULL DEFAULT 0,
  output_tokens    INTEGER NOT NULL DEFAULT 0,
  success          INTEGER NOT NULL DEFAULT 0,
  latency_ms       INTEGER NOT NULL DEFAULT 0,
  render_hint      TEXT,
  trace_id         TEXT,
  created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_cap_usage_ps ON capability_usage(project_space_id, created_at);

CREATE TABLE IF NOT EXISTS capability_domain_agent (
  id               TEXT PRIMARY KEY,
  project_space_id TEXT NOT NULL,
  code             TEXT NOT NULL,
  name             TEXT NOT NULL,
  domain           TEXT NOT NULL DEFAULT 'custom',
  composed_skills  TEXT,
  status           TEXT NOT NULL DEFAULT 'draft',
  created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE (project_space_id, code)
);
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

// SeedDemoSkills 若默认空间的 capability_skill 表为空，播种两条 active demo 技能。
func SeedDemoSkills(ctx context.Context, db *sqlx.DB) error {
	var n int
	if err := db.GetContext(ctx, &n, `SELECT COUNT(*) FROM capability_skill WHERE project_space_id='ps_default'`); err != nil {
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
			 VALUES (?, 'ps_default', ?, ?, ?, ?, ?, '1.0.0', 'active', 'low', 1)`,
			"skl_"+uuid.NewString()[:20], d.code, d.name, d.name+" 技能", d.category, d.prompt); err != nil {
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
