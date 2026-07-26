-- 000018_performance.up.sql
-- 绩效记录：给工作表补 user_id（按人统计前提）+ codews 会话持久化 + 删考勤表。

-- 1) 三张工作表加可空 user_id（创建人；历史存量 NULL → 统计进"未归属"桶）
ALTER TABLE code_task ADD COLUMN IF NOT EXISTS user_id TEXT;
ALTER TABLE change_request ADD COLUMN IF NOT EXISTS user_id TEXT;
ALTER TABLE conversation ADD COLUMN IF NOT EXISTS user_id TEXT;

-- 2) codews 编码工作台会话持久化（原纯内存，绩效/互动统计依赖）
CREATE TABLE IF NOT EXISTS codews_session (
  id TEXT PRIMARY KEY,
  project_space_id TEXT NOT NULL,
  app_id TEXT NOT NULL,
  user_id TEXT, -- 可空（兼容 anonymous）
  tool TEXT NOT NULL, -- opencode/claude/codex
  repo_dir TEXT NOT NULL,
  port INTEGER NOT NULL DEFAULT 0,
  session_id TEXT, -- 工具原生会话 id（opencode 有；claude/codex 按 repo_dir 解析）
  started_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  ended_at TIMESTAMP,
  prompt_count INTEGER NOT NULL DEFAULT 0,
  message_count INTEGER NOT NULL DEFAULT 0,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_codews_session_ps ON codews_session (project_space_id);
CREATE INDEX IF NOT EXISTS idx_codews_session_user ON codews_session (user_id, started_at DESC);

-- 3) 删考勤模块表（模块整体移除，spec 3.7）
DROP TABLE IF EXISTS attendance_record;
