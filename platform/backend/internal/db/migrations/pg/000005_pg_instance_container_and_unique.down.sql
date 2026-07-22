-- 000005_pg_instance_container_and_unique.down.sql
DROP INDEX IF EXISTS uq_pginstance_ps_active;
ALTER TABLE pg_instance DROP COLUMN IF EXISTS container_name;
