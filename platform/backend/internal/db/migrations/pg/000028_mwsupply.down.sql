-- 000028_mwsupply.down.sql
DROP TABLE IF EXISTS appdeploy_service_binding;
DROP TABLE IF EXISTS appdeploy_service_instance;
ALTER TABLE appdeploy_env DROP COLUMN IF EXISTS source;
