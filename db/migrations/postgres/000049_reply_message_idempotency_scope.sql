-- +goose Up
-- reply_message 的幂等键按卡片与单次 Attempt 隔离，避免跨卡及长会话跨轮次误撞冲突，
-- 同时由数据库唯一索引兜底同一次 Attempt 内的幂等唯一性。
CREATE UNIQUE INDEX IF NOT EXISTS events_message_idempotency_uq
  ON events (workspace_id, card_id, attempt_id, actor_source_id, idempotency_key)
  WHERE event_type = 'message'
    AND attempt_id IS NOT NULL
    AND idempotency_key IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS events_message_idempotency_uq;
