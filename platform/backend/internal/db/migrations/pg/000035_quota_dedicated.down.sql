-- 000035_quota_dedicated.down.sql
ALTER TABLE project_quota DROP COLUMN IF EXISTS max_dedicated_instances;
