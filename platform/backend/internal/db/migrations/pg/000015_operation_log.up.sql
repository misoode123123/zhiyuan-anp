-- 操作审计：记录关键操作（谁何时对什么做了什么）。actor 支持 user/agent/system，
-- 为"智能体自动运维"铺路——智能体执行操作时同样被审计。
CREATE TABLE IF NOT EXISTS operation_log (
  id               BIGSERIAL PRIMARY KEY,
  timestamp        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  actor_type       TEXT NOT NULL,    -- user / agent / system
  actor_id         TEXT NOT NULL,    -- 用户 id / agent id / "system"
  action           TEXT NOT NULL,    -- app.deploy / app.delete / change.approve / release.create / quota.update / config.set / auth.login ...
  resource_type    TEXT,             -- app / change / release / requirement / project_space ...
  resource_id      TEXT,
  project_space_id TEXT,
  trace_id         TEXT,
  detail           JSONB,            -- 入参摘要 / 结果 / 版本号等
  status           TEXT NOT NULL,    -- success / failed
  error            TEXT
);
CREATE INDEX IF NOT EXISTS idx_oplog_time ON operation_log(timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_oplog_actor ON operation_log(actor_type, actor_id, timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_oplog_action ON operation_log(action, timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_oplog_resource ON operation_log(resource_type, resource_id);
