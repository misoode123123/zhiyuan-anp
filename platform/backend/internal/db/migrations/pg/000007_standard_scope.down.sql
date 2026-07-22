-- 000007_standard_scope.down.sql
DROP INDEX IF EXISTS idx_std_scope;
ALTER TABLE coding_standard DROP COLUMN IF EXISTS module;
ALTER TABLE coding_standard DROP COLUMN IF EXISTS scope;
