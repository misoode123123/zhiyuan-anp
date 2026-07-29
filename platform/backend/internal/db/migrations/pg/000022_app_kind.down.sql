DROP TABLE IF EXISTS appdeploy_build_config;
DROP TABLE IF EXISTS appdeploy_artifact;
ALTER TABLE appdeploy_application DROP COLUMN IF EXISTS app_kind;
