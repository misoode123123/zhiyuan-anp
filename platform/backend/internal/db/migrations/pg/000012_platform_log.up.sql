-- 统一日志：跨层（前端/后端/Python）错误统一入库
CREATE TABLE IF NOT EXISTS platform_log (
  id              BIGSERIAL PRIMARY KEY,
  timestamp       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  level           TEXT NOT NULL,           -- DEBUG / INFO / WARN / ERROR / FATAL
  source          TEXT NOT NULL,           -- frontend / backend / agent-runtime
  module          TEXT,                    -- requirement / dev / compute / auth / ...
  trace_id        TEXT,
  user_id         TEXT,
  project_space_id TEXT,
  message         TEXT NOT NULL,
  stack_trace     TEXT,
  context         JSONB,                   -- 结构化扩展（method/path/status 等）
  resolved        BOOLEAN NOT NULL DEFAULT FALSE,
  resolved_by     TEXT,
  resolved_at     TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_log_level_time ON platform_log(level, timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_log_trace ON platform_log(trace_id);
CREATE INDEX IF NOT EXISTS idx_log_source_time ON platform_log(source, timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_log_unresolved ON platform_log(timestamp DESC) WHERE resolved = FALSE AND level IN ('ERROR','FATAL');
