-- 000010_app_external.up.sql
-- 存量应用接入（B 类 ① 轻接入）：把已在运行的外部应用纳入 ANP 管理。
-- 代码不动：不 AI 编码 / 不部署 / 不迁库；仅注册 + appgw 统一入口 + ops 按 external_url 探活。
--
-- appdeploy_application:
--   deploy_mode   managed(A类,平台托管) / external(B类,纳管外部)
--   external_url  external 模式时外部应用访问地址（http(s)://host[:port][/path]）
-- appdeploy_route:
--   external_url  非空=external 应用，gateway 直接反代此 URL（managed 为空走 host:port）

ALTER TABLE appdeploy_application
    ADD COLUMN IF NOT EXISTS deploy_mode  TEXT NOT NULL DEFAULT 'managed',
    ADD COLUMN IF NOT EXISTS external_url TEXT NOT NULL DEFAULT '';

ALTER TABLE appdeploy_route
    ADD COLUMN IF NOT EXISTS external_url TEXT NOT NULL DEFAULT '';
