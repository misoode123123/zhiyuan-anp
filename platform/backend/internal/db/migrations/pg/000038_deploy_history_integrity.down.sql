-- 000038_deploy_history_integrity.down.sql
-- 只回滚 DDL。孤儿 DELETE 不可逆（本就该随 app 删除消失），不回填。
DROP INDEX IF EXISTS uq_dephist_inflight;
ALTER TABLE deploy_history DROP CONSTRAINT IF EXISTS deploy_history_app_fk;
