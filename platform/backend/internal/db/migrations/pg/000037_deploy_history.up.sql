CREATE TABLE deploy_history (
  id            BIGSERIAL PRIMARY KEY,
  app_id        TEXT NOT NULL,
  env           TEXT NOT NULL,           -- test / prod
  version       INT  NOT NULL,           -- 部署目标版本号（计数器值，失败不回退）
  engine        TEXT NOT NULL,           -- fixed / ai
  result        TEXT NOT NULL DEFAULT '', -- ''=在途 / success / failed
  operator      TEXT NOT NULL,           -- 触发用户名（CtxUserID）
  sha           TEXT,                    -- 构建提交（有则记，可空）
  image         TEXT,
  host_port     INT,
  duration_sec  INT,                     -- 开始→终态秒数（在途为 NULL）
  error_summary TEXT,                    -- 失败摘要 ≤200 字（last_error 头部）
  notes         TEXT,                    -- AI 链备注：回滚详情/验证失败步/绕过 shim 等
  created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  finished_at   TIMESTAMPTZ
);
CREATE INDEX idx_dephist_app ON deploy_history(app_id, created_at DESC);
CREATE INDEX idx_dephist_stat ON deploy_history(engine, result, created_at DESC);
