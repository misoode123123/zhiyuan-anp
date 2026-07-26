-- 000018_performance.down.sql
-- 回滚：重建考勤表（与 000001 原始定义一致）、删 codews_session、删 user_id 列。

CREATE TABLE IF NOT EXISTS attendance_record (
  id TEXT PRIMARY KEY,
  project_space_id TEXT NOT NULL,
  user_id TEXT NOT NULL,
  status TEXT NOT NULL,
  start_time TIMESTAMP NOT NULL,
  end_time TIMESTAMP NOT NULL,
  reason TEXT,
  supervisor_id TEXT NOT NULL,
  approval_status TEXT NOT NULL DEFAULT 'pending',
  approver TEXT,
  approved_at TIMESTAMP,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_attendance_ps ON attendance_record (project_space_id);
CREATE INDEX IF NOT EXISTS idx_attendance_user ON attendance_record (user_id);
CREATE INDEX IF NOT EXISTS idx_attendance_super ON attendance_record (supervisor_id, approval_status);

DROP TABLE IF EXISTS codews_session;

ALTER TABLE code_task DROP COLUMN IF EXISTS user_id;
ALTER TABLE change_request DROP COLUMN IF EXISTS user_id;
ALTER TABLE conversation DROP COLUMN IF EXISTS user_id;
