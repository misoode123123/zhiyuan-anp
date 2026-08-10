-- 000036: codews 编码对话消息持久化（绩效历史对话可查）。
-- codews_session 仅存 metadata（prompt/message 计数）；对话原文靠 codews Manager 后台快照
-- ticker 从 opencode live API（LiveTranscript）拉取后落此表——进程被 reaper 驱逐/后端重启/
-- 容器重建后，绩效仍可按 session_id 查历史对话（不再"点开历史会话是空的"）。
-- session_id 指向 codews_session.id（cws_xxx）；seq=会话内消息序号，(session_id,seq) 唯一，
-- 支持快照幂等 upsert（ON CONFLICT DO NOTHING）。
CREATE TABLE IF NOT EXISTS codews_message (
  id TEXT PRIMARY KEY,
  session_id TEXT NOT NULL,
  role TEXT NOT NULL,            -- user / assistant
  content TEXT NOT NULL,
  seq INTEGER NOT NULL,          -- 会话内序号（LiveTranscript 返回顺序的 index）
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_codews_message_seq ON codews_message (session_id, seq);
CREATE INDEX IF NOT EXISTS idx_codews_message_session ON codews_message (session_id, seq);
