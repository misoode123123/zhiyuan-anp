-- 000028_mwsupply.up.sql
-- 中间件依赖供给与注入（P1：bind_existing 闭环）
-- 实例注册表 + 每应用绑定 + appdeploy_env.source（区分平台/用户注入）

CREATE TABLE appdeploy_service_instance (
    id               TEXT PRIMARY KEY,
    project_space_id TEXT REFERENCES project_space(id) ON DELETE CASCADE, -- NULL=平台全局
    kind             TEXT NOT NULL,                 -- redis / milvus / ...
    name             TEXT NOT NULL,
    supply_mode      TEXT NOT NULL,                 -- bind_existing / shared / dedicated
    host             TEXT NOT NULL,
    port             INT  NOT NULL,
    auth_ref         TEXT,                          -- 密码/token 引用（明文，同 pgsupply I1 债；阶段3 vault/KMS）
    isolation        JSONB,                         -- 隔离配置（shared 用：redis db_range / milvus prefix）
    status           TEXT NOT NULL DEFAULT 'active',
    created_at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_svinst_ps_kind ON appdeploy_service_instance(project_space_id, kind, supply_mode);
CREATE INDEX idx_svinst_status ON appdeploy_service_instance(status);

CREATE TABLE appdeploy_service_binding (
    id                  TEXT PRIMARY KEY,
    app_id              TEXT NOT NULL REFERENCES appdeploy_application(id) ON DELETE CASCADE,
    project_space_id    TEXT NOT NULL,
    service_kind        TEXT NOT NULL,              -- redis / milvus / ...
    strategy            TEXT NOT NULL,              -- bind_existing / shared / dedicated（解析后）
    service_instance_id TEXT REFERENCES appdeploy_service_instance(id),
    isolation_token     TEXT,                       -- 分配的隔离 token（redis db号 / milvus 前缀）
    env_key             TEXT NOT NULL,              -- REDIS_ADDR / MILVUS_ADDR / ...
    status              TEXT NOT NULL DEFAULT 'declared', -- declared / bound / failed
    last_error          TEXT,
    created_at          TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at          TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (app_id, service_kind)
);
CREATE INDEX idx_svbind_app ON appdeploy_service_binding(app_id);
CREATE INDEX idx_svbind_ps  ON appdeploy_service_binding(project_space_id);

ALTER TABLE appdeploy_env ADD COLUMN IF NOT EXISTS source TEXT NOT NULL DEFAULT 'user';

-- 种子：.28 已有中间件（bind_existing 目标）
INSERT INTO appdeploy_service_instance (id, project_space_id, kind, name, supply_mode, host, port, isolation, status) VALUES
  ('svinst-redis-28',  NULL, 'redis',  'yxt-redis',  'bind_existing', '10.10.0.28', 6381,  '{"default_db":0}'::jsonb, 'active'),
  ('svinst-milvus-28', NULL, 'milvus', 'yxt-milvus', 'bind_existing', '10.10.0.28', 19530, NULL, 'active')
ON CONFLICT (id) DO NOTHING;
