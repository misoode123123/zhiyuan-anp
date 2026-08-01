-- 000029_mwsupply_shared.up.sql
-- P2：shared 共享 redis（db 号隔离）
-- ① 种子平台级 shared redis 实例（复用 .28 同一台 yxt-redis，db 1-15 隔离，db 0 留给 bind_existing/系统）
-- ② 部分唯一索引：防并发分配撞 db 号（兜底；主路径靠乐观选号 + 重试）

INSERT INTO appdeploy_service_instance
  (id, project_space_id, kind, name, supply_mode, host, port, auth_ref, isolation, status)
VALUES
  ('svinst-redis-shared-28', NULL, 'redis', 'yxt-redis-shared', 'shared',
   '10.10.0.28', 6381, NULL, '{"db_range":[1,15]}'::jsonb, 'active')
ON CONFLICT (id) DO NOTHING;

-- 仅对「已分配 token」的 binding 建：NULL 不入索引（多 NULL 不冲突；bind_existing binding token 恒 NULL 不受影响）
CREATE UNIQUE INDEX IF NOT EXISTS uq_svbind_inst_token
  ON appdeploy_service_binding (service_instance_id, isolation_token)
  WHERE isolation_token IS NOT NULL;
