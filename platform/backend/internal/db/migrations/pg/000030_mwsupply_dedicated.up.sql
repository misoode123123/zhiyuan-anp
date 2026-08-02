-- 000030_mwsupply_dedicated.up.sql
-- P3：dedicated 专属 redis（每 app 一个容器）
-- 加 container_name 列：dedicated 实例的容器名（Cleanup 时 docker rm 用）。
-- nullable：bind_existing/shared 种子行及非 dedicated 实例恒 NULL，无影响。

ALTER TABLE appdeploy_service_instance ADD COLUMN IF NOT EXISTS container_name TEXT;
