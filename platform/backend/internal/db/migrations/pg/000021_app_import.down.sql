ALTER TABLE appdeploy_application DROP COLUMN IF EXISTS imported_at;
ALTER TABLE appdeploy_application DROP COLUMN IF EXISTS import_ref;
ALTER TABLE appdeploy_application DROP COLUMN IF EXISTS import_source;
