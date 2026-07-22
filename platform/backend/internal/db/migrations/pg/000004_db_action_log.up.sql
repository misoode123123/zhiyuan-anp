-- 000004_db_action_log.up.sql
-- 数据库操作审计日志：记录每个应用库的 SQL 执行动作（DDL/DML/查询），供「操作日志」展示。

CREATE TABLE db_action_log (
    id               TEXT PRIMARY KEY,
    project_space_id TEXT NOT NULL,
    app_id           TEXT NOT NULL,
    db_name          TEXT NOT NULL,                 -- 目标应用库（app_<hex>）
    actor            TEXT NOT NULL,                 -- 操作人（平台用户名）
    action_type      TEXT NOT NULL,                 -- SELECT / INSERT / UPDATE / DELETE / DDL / OTHER
    statement        TEXT NOT NULL,                 -- 执行的 SQL（截断存储，防超长）
    row_count        INT  NOT NULL DEFAULT 0,       -- 影响行数（DML）/ 返回行数（SELECT）
    status           TEXT NOT NULL,                 -- success / failed
    error            TEXT,                          -- 失败原因
    trace_id         TEXT,
    created_at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_dbaction_app ON db_action_log(app_id, created_at DESC);
CREATE INDEX idx_dbaction_ps ON db_action_log(project_space_id);
