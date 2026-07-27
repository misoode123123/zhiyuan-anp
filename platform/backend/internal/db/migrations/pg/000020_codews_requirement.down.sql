DROP INDEX IF EXISTS idx_codews_session_req;
ALTER TABLE codews_session DROP COLUMN IF EXISTS requirement_id;
