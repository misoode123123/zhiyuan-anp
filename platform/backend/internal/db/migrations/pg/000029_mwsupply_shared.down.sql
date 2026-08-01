-- 000029_mwsupply_shared.down.sql
DROP INDEX IF EXISTS uq_svbind_inst_token;
DELETE FROM appdeploy_service_instance WHERE id = 'svinst-redis-shared-28';
