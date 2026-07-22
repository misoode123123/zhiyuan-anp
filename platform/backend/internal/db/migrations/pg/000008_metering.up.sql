-- 000008_metering.up.sql
-- 阶段3b：计量采集 —— appgw 应用 API 调用日志 + 库大小定时采集

-- appgw_access_log：每次 /apps/<code>/ 反代记一笔（status/latency/caller）
-- 用于 3c 看板「应用 API 调用量」数据源；本阶段只采集 + 原始 API。
CREATE TABLE appgw_access_log (
    id               TEXT PRIMARY KEY,
    project_space_id TEXT NOT NULL,
    app_id           TEXT NOT NULL,
    app_code         TEXT NOT NULL,
    env              TEXT NOT NULL,
    caller           TEXT,                 -- 鉴权用户 / apikey:<id前缀> / anonymous（可空）
    method           TEXT NOT NULL,
    path             TEXT NOT NULL,        -- 原始 /apps/<code>/... 请求路径
    status           INT  NOT NULL,        -- upstream HTTP 状态（502 = 反代失败/upstream 不可达）
    latency_ms       INT  NOT NULL,
    trace_id         TEXT,
    created_at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_accesslog_app ON appgw_access_log(app_id, created_at DESC);
CREATE INDEX idx_accesslog_ps  ON appgw_access_log(project_space_id, created_at DESC);

-- 库大小定时采集：CollectDBSizes 每 tick 更新（连 PG 实例查 pg_database_size）
ALTER TABLE appdeploy_database ADD COLUMN size_bytes BIGINT NOT NULL DEFAULT 0;
