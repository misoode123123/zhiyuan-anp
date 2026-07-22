-- 000006_appdeploy_route.up.sql
-- 阶段2：应用 API 统一入口 —— appdeploy_route 路由表（appgw 消费）

CREATE TABLE appdeploy_route (
    id               TEXT PRIMARY KEY,
    app_id           TEXT NOT NULL REFERENCES appdeploy_application(id) ON DELETE CASCADE,
    project_space_id TEXT NOT NULL,
    app_code         TEXT NOT NULL,                 -- URL 路径段：/apps/<app_code>/（用 app_id，唯一无特殊字符）
    env              TEXT NOT NULL,                 -- test / prod
    upstream_host    TEXT NOT NULL,                 -- 应用容器可达 host（APPDEPLOY_HOST）
    upstream_port    INT  NOT NULL,                 -- 应用容器端口（appdeploy 分配的 host_port）
    status           TEXT NOT NULL,                 -- active / inactive
    auth_required    BOOLEAN NOT NULL DEFAULT TRUE, -- 是否强制平台身份（true 时 appgw 验 JWT）
    created_at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(app_code, env)
);
CREATE INDEX idx_route_code ON appdeploy_route(app_code, env) WHERE status='active';
