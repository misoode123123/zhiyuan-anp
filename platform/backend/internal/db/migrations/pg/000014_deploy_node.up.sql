-- 部署节点：多机部署（.28 本地 + .30/.31 远程）
CREATE TABLE IF NOT EXISTS deploy_node (
  id          TEXT PRIMARY KEY,
  name        TEXT NOT NULL,
  host        TEXT NOT NULL,           -- IP（如 10.10.0.30）
  docker_url  TEXT NOT NULL,           -- tcp://10.10.0.30:2375
  ssh_user    TEXT NOT NULL DEFAULT 'root',
  status      TEXT NOT NULL DEFAULT 'active',  -- active / offline
  max_apps    INTEGER NOT NULL DEFAULT 20,
  description TEXT,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 默认本地节点
INSERT INTO deploy_node (id, name, host, docker_url, description)
VALUES ('node_local', '本地节点', '127.0.0.1', '', '平台主节点（.28）')
ON CONFLICT (id) DO NOTHING;

-- 应用表加 deploy_node_id 列
ALTER TABLE appdeploy_application ADD COLUMN IF NOT EXISTS deploy_node_id TEXT REFERENCES deploy_node(id);
