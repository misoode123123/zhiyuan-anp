-- 000020: codews_session 绑定 requirement_id（工作直播按需求查 worker 会话）。
-- 兼容存量与 application 页老入口（不带 requirement_id）：列为可空。
ALTER TABLE codews_session ADD COLUMN IF NOT EXISTS requirement_id TEXT;
CREATE INDEX IF NOT EXISTS idx_codews_session_req
  ON codews_session (project_space_id, requirement_id);
