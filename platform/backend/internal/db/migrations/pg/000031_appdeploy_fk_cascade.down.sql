-- 000031_appdeploy_fk_cascade.down.sql
-- 只回滚 DDL。历史孤儿 DELETE 不可逆（且本就该随 app 删除消失），不回填。
ALTER TABLE appdeploy_instance DROP CONSTRAINT IF EXISTS appdeploy_instance_app_fk;
ALTER TABLE appdeploy_env      DROP CONSTRAINT IF EXISTS appdeploy_env_app_fk;
