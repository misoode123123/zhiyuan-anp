-- 000035_quota_dedicated.up.sql
-- 依赖供给统一 P4：project_quota 加第 5 维度「专属实例数上限」。
-- dedicated 实例（redis/milvus/pg）合计 per 项目空间，默认 5；T3 admin 可调。
-- 幂等：ADD COLUMN IF NOT EXISTS（回放不报错）。
ALTER TABLE project_quota ADD COLUMN IF NOT EXISTS max_dedicated_instances INT NOT NULL DEFAULT 5;
