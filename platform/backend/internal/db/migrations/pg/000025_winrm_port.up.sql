-- 修复 WinRM 端口硬编码 5985：deploy_node 增 winrm_port 列。
-- 既有节点 winrm_port 默认 5985；新建 WinRM 节点时前端按 winrm_port 填写。
ALTER TABLE deploy_node ADD COLUMN IF NOT EXISTS winrm_port INT NOT NULL DEFAULT 5985;
