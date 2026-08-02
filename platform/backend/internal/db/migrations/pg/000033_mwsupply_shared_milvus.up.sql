-- 000033_mwsupply_shared_milvus.up.sql
-- P5：shared 共享 milvus（collection 前缀隔离）
-- ① 种子一条平台级 shared milvus 实例（复用 .28 同一台 yxt-milvus，每 app 独占一个 collection 前缀）
-- ② 唯一索引 uq_svbind_inst_token（000029 已建）直接复用——token=前缀串，两 app 前缀不同不冲突，无需新索引

INSERT INTO appdeploy_service_instance
  (id, project_space_id, kind, name, supply_mode, host, port, auth_ref, isolation, status)
VALUES
  ('svinst-milvus-shared-28', NULL, 'milvus', 'yxt-milvus-shared', 'shared',
   '10.10.0.28', 19530, NULL, '{"mode":"prefix"}'::jsonb, 'active')
ON CONFLICT (id) DO NOTHING;
