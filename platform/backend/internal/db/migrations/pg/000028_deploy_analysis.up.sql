CREATE TABLE IF NOT EXISTS appdeploy_deploy_analysis (
    app_id      TEXT PRIMARY KEY,
    analysis    JSONB NOT NULL,
    analyzed_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
