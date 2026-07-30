-- I4: provision 日志持久化（spec §4.4）。
-- SetNodeStatus 需记录 Provisioner 输出的 buildLog，便于前端展示 provision 失败/成功详情。
ALTER TABLE deploy_node ADD COLUMN IF NOT EXISTS provision_log TEXT;
