-- docker_tcp 节点采集容器数:appdeploy_server_metric 增 container_count 列。
-- 非 docker 节点该列默认 0,前端按 connect_type 决定是否展示。
ALTER TABLE appdeploy_server_metric ADD COLUMN IF NOT EXISTS container_count INT NOT NULL DEFAULT 0;
