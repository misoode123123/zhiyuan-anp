-- host 网络门禁（PRD §7 中期-运行态 ④）：Application 加 network_mode 维度。
-- bridge 默认；host 需 gatekeeper/admin 角色开启+部署（op app.net.host）。与 app_kind/deploy_mode 正交。
ALTER TABLE appdeploy_application
    ADD COLUMN IF NOT EXISTS network_mode TEXT NOT NULL DEFAULT 'bridge';
-- 值: bridge(默认) / host
