-- 000009_db_size_snapshot.up.sql
-- 阶段3c：库大小历史快照 —— 让「库总大小」有日级趋势（3b 的 appdeploy_database.size_bytes
-- 只是当前值，无历史）。pgsupply.Collector 每 tick（默认 1h）采完库大小，顺手按项目
-- 插一条 total_size_bytes 快照。3c 看板用此表画「库大小增长」折线。

CREATE TABLE db_size_snapshot (
    id                TEXT PRIMARY KEY,
    project_space_id  TEXT NOT NULL,
    total_size_bytes  BIGINT NOT NULL,            -- 该项目当时所有非 deleted 库 size_bytes 之和
    created_at        TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_dbsnapshot_ps_created ON db_size_snapshot(project_space_id, created_at DESC);
