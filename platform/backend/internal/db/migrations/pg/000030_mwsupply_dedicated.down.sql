-- 000030_mwsupply_dedicated.down.sql
ALTER TABLE appdeploy_service_instance DROP COLUMN IF EXISTS container_name;
