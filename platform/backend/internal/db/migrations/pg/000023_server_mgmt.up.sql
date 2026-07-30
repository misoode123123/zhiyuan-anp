-- 服务器管理板块：deploy_node 扩展（OS/env/连接类型/凭证）+ 监督指标表。
-- 现有 docker_tcp 节点零影响：os_type/env/connect_type 默认值兼容。

ALTER TABLE deploy_node ADD COLUMN IF NOT EXISTS os_type TEXT NOT NULL DEFAULT 'linux';
ALTER TABLE deploy_node ADD COLUMN IF NOT EXISTS env TEXT NOT NULL DEFAULT 'dev';
ALTER TABLE deploy_node ADD COLUMN IF NOT EXISTS connect_type TEXT NOT NULL DEFAULT 'docker_tcp';
ALTER TABLE deploy_node ADD COLUMN IF NOT EXISTS ssh_port INT NOT NULL DEFAULT 22;
ALTER TABLE deploy_node ADD COLUMN IF NOT EXISTS ssh_key TEXT;
ALTER TABLE deploy_node ADD COLUMN IF NOT EXISTS winrm_user TEXT;
ALTER TABLE deploy_node ADD COLUMN IF NOT EXISTS winrm_password TEXT;
ALTER TABLE deploy_node ADD COLUMN IF NOT EXISTS last_seen TIMESTAMPTZ;

-- .28 node_local 是测试环境
UPDATE deploy_node SET env='test' WHERE id='node_local';

CREATE TABLE IF NOT EXISTS appdeploy_server_metric (
    node_id     TEXT NOT NULL,
    captured_at TIMESTAMPTZ NOT NULL,
    cpu_percent REAL NOT NULL,
    mem_total   BIGINT NOT NULL,
    mem_used    BIGINT NOT NULL,
    disk_total  BIGINT NOT NULL,
    disk_used   BIGINT NOT NULL,
    load_avg    REAL,
    uptime      TEXT,
    app_count   INT NOT NULL DEFAULT 0,
    PRIMARY KEY (node_id, captured_at)
);
CREATE INDEX IF NOT EXISTS idx_metric_node_time ON appdeploy_server_metric(node_id, captured_at DESC);
