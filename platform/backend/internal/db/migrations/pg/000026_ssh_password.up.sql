-- Windows 走 SSH 采集:go-ntlmssp 库与现代 Windows Server NTLM 不兼容(WinRM type3 被拒),
-- 改用 OpenSSH 到 Windows 采集。deploy_node 增 ssh_password 列(SSHExecutor 密码认证用)。
-- 既有 ssh 节点(走 key)ssh_password 为空,SSHExecutor 空密码回退 key 认证,不影响。
ALTER TABLE deploy_node ADD COLUMN IF NOT EXISTS ssh_password TEXT NOT NULL DEFAULT '';
