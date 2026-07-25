-- 消息通知：审批/编码/发布等事件实时推送
CREATE TABLE IF NOT EXISTS notification (
  id           BIGSERIAL PRIMARY KEY,
  user_id      TEXT,                    -- 接收者（null = 广播给全员）
  project_space_id TEXT,
  type         TEXT NOT NULL,           -- code_done / change_pending / change_decided / release / system
  title        TEXT NOT NULL,
  message      TEXT,
  link         TEXT,                    -- 点击跳转路径
  read         BOOLEAN NOT NULL DEFAULT FALSE,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_notif_user_unread ON notification(user_id, read, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_notif_type ON notification(type, created_at DESC);
