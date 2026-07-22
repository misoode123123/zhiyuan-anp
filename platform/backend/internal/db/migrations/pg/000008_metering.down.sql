-- 000008_metering.down.sql
-- 回滚 3b 计量采集

ALTER TABLE appdeploy_database DROP COLUMN IF EXISTS size_bytes;

DROP TABLE IF EXISTS appgw_access_log;
