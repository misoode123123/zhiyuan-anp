-- 000005_pg_instance_container_and_unique.up.sql
-- 1) pg_instance 加 container_name：provision 起的 PG 容器名，供备份(docker exec pg_dump) + 删项目级联清理(docker rm -f)。
-- 2) partial unique index：每个 project_space 至多 1 个 active 实例，兜底同项目并发建应用起多 PG 容器。
-- 已存在 active 实例的项目不会被本迁移阻塞（数据不变；新冲突由应用层捕获并 fallback 重查）。

ALTER TABLE pg_instance ADD COLUMN IF NOT EXISTS container_name TEXT;

CREATE UNIQUE INDEX IF NOT EXISTS uq_pginstance_ps_active
    ON pg_instance(project_space_id) WHERE status = 'active';
