-- 000010_app_external.down.sql
-- 回滚 external 字段（先 route 后 application，对称）

ALTER TABLE appdeploy_route DROP COLUMN IF EXISTS external_url;

ALTER TABLE appdeploy_application DROP COLUMN IF EXISTS external_url;
ALTER TABLE appdeploy_application DROP COLUMN IF EXISTS deploy_mode;
