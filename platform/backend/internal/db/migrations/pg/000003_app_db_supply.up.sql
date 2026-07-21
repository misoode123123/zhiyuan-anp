-- 000003_app_db_supply.up.sql
-- 阶段1：应用库统一供给（每项目一个独立 PG 实例 + 应用库供给记录 + 项目配额）

-- pg_instance：PG 实例注册表（每 project_space 一个独立 PG 实例；平台纳管）
CREATE TABLE pg_instance (
    id               TEXT PRIMARY KEY,
    project_space_id TEXT NOT NULL REFERENCES project_space(id) ON DELETE CASCADE,
    host             TEXT NOT NULL,
    port             INT  NOT NULL DEFAULT 5432,
    admin_url_ref    TEXT NOT NULL,                 -- superuser 连接串(含密码),不对外暴露
    deploy_mode      TEXT NOT NULL DEFAULT 'managed', -- managed(docker起容器) / external(纳管远程)
    status           TEXT NOT NULL DEFAULT 'active',  -- active / draining / maintenance
    created_at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_pginst_ps ON pg_instance(project_space_id);
CREATE INDEX idx_pginst_status ON pg_instance(status);

-- appdeploy_database：应用库供给记录（库元数据 + 生命周期 + 备份/迁移版本）
CREATE TABLE appdeploy_database (
    id               TEXT PRIMARY KEY,
    app_id           TEXT NOT NULL REFERENCES appdeploy_application(id) ON DELETE CASCADE,
    project_space_id TEXT NOT NULL,
    db_name          TEXT NOT NULL,                 -- app_<shortid>
    db_role          TEXT NOT NULL,                 -- 专用 role（仅本库权限）
    pg_instance_id   TEXT NOT NULL REFERENCES pg_instance(id) ON DELETE CASCADE,
    db_host          TEXT NOT NULL,
    db_port          INT  NOT NULL DEFAULT 5432,
    status           TEXT NOT NULL DEFAULT 'provisioning', -- provisioning/ready/failed/deleted
    last_error       TEXT,
    backup_enabled   BOOLEAN NOT NULL DEFAULT TRUE,
    last_backup_at   TIMESTAMP,
    schema_version   TEXT,
    created_at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(db_name),
    UNIQUE(app_id)
);
CREATE INDEX idx_appdb_ps ON appdeploy_database(project_space_id);

-- project_quota：项目配额（阶段3强制；阶段1仅建表 + 默认值）
CREATE TABLE project_quota (
    project_space_id             TEXT PRIMARY KEY REFERENCES project_space(id) ON DELETE CASCADE,
    max_apps                     INT NOT NULL DEFAULT 20,
    max_databases                INT NOT NULL DEFAULT 20,
    max_total_db_mb              INT NOT NULL DEFAULT 10240,
    max_capability_calls_per_day INT NOT NULL DEFAULT 10000,
    updated_at                   TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
