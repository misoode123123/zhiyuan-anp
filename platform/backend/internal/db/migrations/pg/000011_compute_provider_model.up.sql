-- 算力中心：多平台多模型统一网关（P0：provider/model/route + usage 扩展）

CREATE TABLE IF NOT EXISTS compute_provider (
  id          TEXT PRIMARY KEY,
  name        TEXT NOT NULL,
  type        TEXT NOT NULL DEFAULT 'api',   -- api / local
  base_url    TEXT NOT NULL,
  api_key     TEXT,
  enabled     BOOLEAN NOT NULL DEFAULT TRUE,
  config      JSONB,                          -- supplier 特定配置
  description TEXT,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS compute_model (
  id             TEXT PRIMARY KEY,
  provider_id    TEXT NOT NULL REFERENCES compute_provider(id) ON DELETE CASCADE,
  name           TEXT NOT NULL,               -- glm-5.1 / gpt-4o
  display_name   TEXT,
  modality       TEXT NOT NULL DEFAULT 'text', -- text / vision / code
  context_window INTEGER,
  max_output     INTEGER,
  cost_input     REAL NOT NULL DEFAULT 0,     -- 每千 token 输入单价
  cost_output    REAL NOT NULL DEFAULT 0,
  capabilities   JSONB,                       -- ["reasoning","vision"]
  enabled        BOOLEAN NOT NULL DEFAULT TRUE,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE(provider_id, name)
);

CREATE TABLE IF NOT EXISTS compute_route (
  id                TEXT PRIMARY KEY,
  task_type         TEXT NOT NULL UNIQUE,     -- spec/code/test/review/chat/general
  primary_model_id  TEXT NOT NULL REFERENCES compute_model(id),
  fallback_model_id TEXT REFERENCES compute_model(id),
  priority          INTEGER NOT NULL DEFAULT 0,
  enabled           BOOLEAN NOT NULL DEFAULT TRUE,
  updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- usage_record 扩展（加 provider/model/cost 维度）
ALTER TABLE usage_record ADD COLUMN IF NOT EXISTS provider_id TEXT;
ALTER TABLE usage_record ADD COLUMN IF NOT EXISTS model_id    TEXT;
ALTER TABLE usage_record ADD COLUMN IF NOT EXISTS cost        REAL NOT NULL DEFAULT 0;
