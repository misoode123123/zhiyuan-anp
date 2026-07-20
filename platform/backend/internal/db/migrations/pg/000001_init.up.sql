-- 智源 ANP · PostgreSQL 初始 schema（由 SQLite sqliteSchema 转换 + 增量列合并）。
-- 主要差异：DATETIME→TIMESTAMP；INTEGER 保留（兼容 Go int 扫描）；TEXT 主键（uuid）保留。
-- 增量列（requirement.tasks/assignee、change_request/release_record.application_id）直接并入。

CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE IF NOT EXISTS project_space (
  id         TEXT PRIMARY KEY,
  name       TEXT NOT NULL,
  slug       TEXT NOT NULL UNIQUE,
  status     TEXT NOT NULL DEFAULT 'active',
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS project (
  id               TEXT PRIMARY KEY,
  project_space_id TEXT NOT NULL REFERENCES project_space(id),
  name             TEXT NOT NULL,
  slug             TEXT NOT NULL,
  status           TEXT NOT NULL DEFAULT 'active',
  created_at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE (project_space_id, slug)
);
CREATE INDEX IF NOT EXISTS idx_project_space ON project(project_space_id);

CREATE TABLE IF NOT EXISTS membership (
  id               TEXT PRIMARY KEY,
  project_space_id TEXT NOT NULL REFERENCES project_space(id),
  user_id          TEXT NOT NULL,
  role             TEXT NOT NULL,
  created_at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE (project_space_id, user_id)
);

CREATE TABLE IF NOT EXISTS "user" (
  id            TEXT PRIMARY KEY,
  name          TEXT NOT NULL UNIQUE,
  email         TEXT,
  password_hash TEXT,
  status        TEXT NOT NULL DEFAULT 'active',
  created_at    TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS requirement (
  id                  TEXT PRIMARY KEY,
  project_space_id    TEXT NOT NULL,
  application_id      TEXT,        -- 归属应用(发布自动部署后回填)
  title               TEXT NOT NULL,
  description         TEXT,
  user_story          TEXT,
  acceptance_criteria TEXT,
  status              TEXT NOT NULL DEFAULT 'draft',
  priority            TEXT,        -- 优先级
  fixed_version       TEXT,        -- 计划版本
  tasks               TEXT,        -- JSON 子任务清单(AI 拆解)
  assignee            TEXT,        -- 认领的开发者(认领互斥)
  assigned_at         TIMESTAMP,
  created_at          TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at          TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_requirement_ps ON requirement(project_space_id);

CREATE TABLE IF NOT EXISTS system_config (
  key         TEXT PRIMARY KEY,
  value       TEXT,
  category    TEXT NOT NULL DEFAULT 'general',
  description TEXT,
  updated_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS rule (
  id              TEXT PRIMARY KEY,
  name            TEXT NOT NULL,
  category        TEXT NOT NULL DEFAULT 'general',
  type            TEXT NOT NULL DEFAULT 'mandatory',
  condition       TEXT NOT NULL,
  condition_field TEXT NOT NULL DEFAULT 'prompt',
  action          TEXT NOT NULL DEFAULT 'block',
  scope           TEXT NOT NULL DEFAULT 'all',
  enabled BOOLEAN NOT NULL DEFAULT TRUE,
  description     TEXT,
  created_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS change_request (
  id              TEXT PRIMARY KEY,
  project_space_id TEXT,
  kind            TEXT NOT NULL DEFAULT 'code',
  source_id       TEXT,
  application_id  TEXT,
  repo_dir        TEXT,
  prompt          TEXT,
  model           TEXT,
  output          TEXT,
  status          TEXT NOT NULL DEFAULT 'pending',
  reviewer        TEXT,
  reviewed_at     TIMESTAMP,
  created_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS test_case (
  id               TEXT PRIMARY KEY,
  project_space_id TEXT NOT NULL,
  requirement_id   TEXT,
  title            TEXT NOT NULL,
  steps            TEXT,
  expected         TEXT,
  status           TEXT NOT NULL DEFAULT 'draft',
  method           TEXT,
  path             TEXT,
  expected_status  INTEGER,
  expected_body    TEXT,
  actual_status    INTEGER,
  actual_body      TEXT,
  run_at           TIMESTAMP,
  created_at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_test_case_ps ON test_case(project_space_id);

CREATE TABLE IF NOT EXISTS release_record (
  id               TEXT PRIMARY KEY,
  project_space_id TEXT NOT NULL,
  change_id        TEXT,
  application_id   TEXT,
  version          TEXT NOT NULL,
  status           TEXT NOT NULL DEFAULT 'released',
  created_at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS usage_record (
  id                TEXT PRIMARY KEY,
  project_space_id  TEXT NOT NULL,
  model             TEXT,
  kind              TEXT,
  prompt_tokens     INTEGER NOT NULL DEFAULT 0,
  completion_tokens INTEGER NOT NULL DEFAULT 0,
  total_tokens      INTEGER NOT NULL DEFAULT 0,
  created_at        TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
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
  status           TEXT NOT NULL DEFAULT 'running',
  output           TEXT,
  change_id        TEXT,
  created_at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS coding_standard (
  id               TEXT PRIMARY KEY,
  project_space_id TEXT NULL,
  name             TEXT NOT NULL,
  category         TEXT NOT NULL DEFAULT 'general',
  content          TEXT NOT NULL,
  priority         INTEGER NOT NULL DEFAULT 100,
  enabled BOOLEAN NOT NULL DEFAULT TRUE,
  created_at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_coding_standard_ps ON coding_standard(project_space_id);

CREATE TABLE IF NOT EXISTS conversation (
  id               TEXT PRIMARY KEY,
  project_space_id TEXT NOT NULL REFERENCES project_space(id),
  status           TEXT NOT NULL DEFAULT 'active',
  title            TEXT,
  requirement_id   TEXT,
  created_at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_conversation_ps ON conversation(project_space_id);

CREATE TABLE IF NOT EXISTS message (
  id              TEXT PRIMARY KEY,
  conversation_id TEXT NOT NULL REFERENCES conversation(id),
  role            TEXT NOT NULL,
  content         TEXT NOT NULL,
  media_kind      TEXT NOT NULL DEFAULT 'text',
  created_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_message_conv ON message(conversation_id);

CREATE TABLE IF NOT EXISTS ops_alert (
  id               TEXT PRIMARY KEY,
  project_space_id TEXT NOT NULL,
  source           TEXT NOT NULL DEFAULT 'custom',
  severity         TEXT NOT NULL DEFAULT 'warning',
  status           TEXT NOT NULL DEFAULT 'firing',
  fingerprint      TEXT NOT NULL,
  title            TEXT NOT NULL,
  description      TEXT,
  fired_at         TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  resolved_at      TIMESTAMP,
  created_at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_ops_alert_ps ON ops_alert(project_space_id, status);
CREATE INDEX IF NOT EXISTS idx_ops_alert_fp ON ops_alert(fingerprint);

CREATE TABLE IF NOT EXISTS ops_sop (
  id                TEXT PRIMARY KEY,
  project_space_id  TEXT NOT NULL,
  code              TEXT NOT NULL,
  name              TEXT NOT NULL,
  description       TEXT,
  category          TEXT NOT NULL DEFAULT 'restart',
  risk_level        TEXT NOT NULL DEFAULT 'low',
  steps             TEXT,
  rollback          TEXT,
  requires_approval BOOLEAN NOT NULL DEFAULT FALSE,
  status            TEXT NOT NULL DEFAULT 'draft',
  created_at        TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at        TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE (project_space_id, code)
);
CREATE INDEX IF NOT EXISTS idx_ops_sop_ps ON ops_sop(project_space_id, status);

CREATE TABLE IF NOT EXISTS security_scan_result (
  id               TEXT PRIMARY KEY,
  project_space_id TEXT NOT NULL,
  scan_type        TEXT NOT NULL,
  risk_level       TEXT NOT NULL DEFAULT 'clean',
  total_findings   INTEGER NOT NULL DEFAULT 0,
  critical_count   INTEGER NOT NULL DEFAULT 0,
  high_count       INTEGER NOT NULL DEFAULT 0,
  medium_count     INTEGER NOT NULL DEFAULT 0,
  low_count        INTEGER NOT NULL DEFAULT 0,
  content_preview  TEXT,
  created_at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_sec_scan_ps ON security_scan_result(project_space_id);

CREATE TABLE IF NOT EXISTS security_finding (
  id               TEXT PRIMARY KEY,
  project_space_id TEXT NOT NULL,
  scan_result_id   TEXT NOT NULL REFERENCES security_scan_result(id),
  category         TEXT NOT NULL,
  rule_id          TEXT NOT NULL,
  severity         TEXT NOT NULL,
  title            TEXT NOT NULL,
  description      TEXT,
  line_number      INTEGER,
  code_snippet     TEXT,
  remediation      TEXT,
  confidence       REAL NOT NULL DEFAULT 1.0,
  status           TEXT NOT NULL DEFAULT 'open',
  created_at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  suppressed_at    TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_sec_finding_ps ON security_finding(project_space_id, status, severity);

CREATE TABLE IF NOT EXISTS security_data_classification (
  id                TEXT PRIMARY KEY,
  project_space_id  TEXT NOT NULL,
  field_name        TEXT NOT NULL,
  table_ref         TEXT NOT NULL,
  sensitivity_level TEXT NOT NULL DEFAULT 'internal',
  data_type         TEXT NOT NULL DEFAULT 'pii',
  masking_strategy  TEXT NOT NULL DEFAULT 'mask',
  status            TEXT NOT NULL DEFAULT 'draft',
  created_at        TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_sec_dc_ps ON security_data_classification(project_space_id);

CREATE TABLE IF NOT EXISTS security_audit (
  id               TEXT PRIMARY KEY,
  project_space_id TEXT NOT NULL,
  actor_type       TEXT NOT NULL,
  actor_id         TEXT,
  action           TEXT NOT NULL,
  resource_type    TEXT,
  detail           TEXT,
  policy_decision  TEXT,
  created_at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_sec_audit_ps ON security_audit(project_space_id);

CREATE TABLE IF NOT EXISTS capability_skill (
  id                TEXT PRIMARY KEY,
  project_space_id  TEXT NOT NULL,
  code              TEXT NOT NULL,
  name              TEXT NOT NULL,
  description       TEXT,
  category          TEXT NOT NULL DEFAULT 'assistant',
  prompt_template   TEXT,
  version           TEXT NOT NULL DEFAULT '0.1.0',
  status            TEXT NOT NULL DEFAULT 'draft',
  risk_level        TEXT NOT NULL DEFAULT 'low',
  is_public BOOLEAN NOT NULL DEFAULT FALSE,
  data_access_scope TEXT,
  created_at        TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at        TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
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
  expires_at       TIMESTAMP,
  created_at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
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
  success BOOLEAN NOT NULL DEFAULT FALSE,
  latency_ms       INTEGER NOT NULL DEFAULT 0,
  render_hint      TEXT,
  trace_id         TEXT,
  created_at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
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
  created_at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE (project_space_id, code)
);

CREATE TABLE IF NOT EXISTS attendance_record (
  id               TEXT PRIMARY KEY,
  project_space_id TEXT NOT NULL,
  user_id          TEXT NOT NULL,
  status           TEXT NOT NULL,
  start_time       TIMESTAMP NOT NULL,
  end_time         TIMESTAMP NOT NULL,
  reason           TEXT,
  supervisor_id    TEXT NOT NULL,
  approval_status  TEXT NOT NULL DEFAULT 'pending',
  approver         TEXT,
  approved_at      TIMESTAMP,
  created_at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_attendance_ps ON attendance_record(project_space_id);
CREATE INDEX IF NOT EXISTS idx_attendance_user ON attendance_record(user_id);
CREATE INDEX IF NOT EXISTS idx_attendance_super ON attendance_record(supervisor_id, approval_status);

CREATE TABLE IF NOT EXISTS appdeploy_application (
  id               TEXT PRIMARY KEY,
  project_space_id TEXT NOT NULL,
  name             TEXT NOT NULL,
  repo_dir         TEXT,
  internal_port    INTEGER NOT NULL DEFAULT 80,
  image            TEXT,
  container_name   TEXT,
  host_port        INTEGER NOT NULL DEFAULT 0,
  url              TEXT,
  version          INTEGER NOT NULL DEFAULT 0,
  status           TEXT NOT NULL DEFAULT 'registered',
  last_error       TEXT,
  build_log        TEXT,
  created_at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE (project_space_id, name)
);
CREATE INDEX IF NOT EXISTS idx_appdeploy_ps ON appdeploy_application(project_space_id);

CREATE TABLE IF NOT EXISTS appdeploy_instance (
  id             TEXT PRIMARY KEY,
  app_id         TEXT NOT NULL,
  env            TEXT NOT NULL,
  image          TEXT,
  container_name TEXT,
  host_port      INTEGER NOT NULL DEFAULT 0,
  url            TEXT,
  version        INTEGER NOT NULL DEFAULT 0,
  status         TEXT NOT NULL DEFAULT 'registered',
  last_error     TEXT,
  build_log      TEXT,
  created_at     TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at     TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE (app_id, env)
);
CREATE INDEX IF NOT EXISTS idx_appdeploy_instance_app ON appdeploy_instance(app_id);

CREATE TABLE IF NOT EXISTS appdeploy_env (
  id         TEXT PRIMARY KEY,
  app_id     TEXT NOT NULL,
  key        TEXT NOT NULL,
  value      TEXT,
  is_secret BOOLEAN NOT NULL DEFAULT FALSE,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE (app_id, key)
);
CREATE INDEX IF NOT EXISTS idx_appdeploy_env_app ON appdeploy_env(app_id);

CREATE TABLE IF NOT EXISTS auth_session (
  token      TEXT PRIMARY KEY,
  user_id    TEXT NOT NULL,
  user_name  TEXT NOT NULL,
  expires_at TIMESTAMP NOT NULL,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_auth_session_user ON auth_session(user_id);

-- schema_migrations 表由 golang-migrate 自行管理。
