-- 回滚 000002：移除 requirement 的三列（与 up 对称）。
ALTER TABLE requirement DROP COLUMN IF EXISTS application_id;
ALTER TABLE requirement DROP COLUMN IF EXISTS priority;
ALTER TABLE requirement DROP COLUMN IF EXISTS fixed_version;
