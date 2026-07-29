-- 非 web 应用形态：Application 加 app_kind 维度 + 产物表 + 构建配置表。
-- app_kind 与 deploy_mode 正交；web 链路不变，存量数据默认 web。

ALTER TABLE appdeploy_application
    ADD COLUMN IF NOT EXISTS app_kind TEXT NOT NULL DEFAULT 'web';
-- 值: web / desktop / mobile / cli / service

CREATE TABLE IF NOT EXISTS appdeploy_artifact (
    id             TEXT PRIMARY KEY,          -- art_xxx
    application_id TEXT NOT NULL,            -- 关联应用
    build_version  INT NOT NULL,              -- 对应 Application.Version
    app_kind       TEXT NOT NULL,             -- 冗余,便于按形态查询
    platform       TEXT NOT NULL,             -- windows / macos / linux / android / ios / multi
    arch           TEXT NOT NULL,             -- x64 / arm64 / x86 / universal / multi
    filename       TEXT NOT NULL,             -- myapp-1.0.0-win-x64.exe
    size_bytes     BIGINT NOT NULL,
    sha256         TEXT NOT NULL,             -- 完整性校验
    storage_key    TEXT NOT NULL,             -- MinIO 对象 key
    content_type   TEXT,                       -- application/octet-stream 等
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (application_id) REFERENCES appdeploy_application(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_artifact_app_ver ON appdeploy_artifact(application_id, build_version);

CREATE TABLE IF NOT EXISTS appdeploy_build_config (
    app_kind      TEXT PRIMARY KEY,            -- desktop/mobile/cli/service
    build_image   TEXT NOT NULL,               -- anp/builder-electron:latest
    build_command TEXT NOT NULL,               -- cd /src && npm ci && npx electron-builder ...
    artifact_dir  TEXT NOT NULL,               -- /src/dist
    scaffold      TEXT NOT NULL,               -- electron-react-ts
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
