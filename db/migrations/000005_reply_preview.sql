-- +goose Up
-- 流式正文是用户已经看见的 Message；完整工具调用提交前保留可更新的快照。
CREATE UNIQUE INDEX events_reply_preview_key
ON events (workspace_id, card_id, attempt_id, (message->>'preview_id'))
WHERE message ? 'preview_id';
ALTER TABLE events ADD CONSTRAINT events_reply_preview_check CHECK (
  NOT (message ? 'preview_id') OR (
    event_type = 'message' AND attempt_id IS NOT NULL
    AND jsonb_typeof(message->'preview_id') = 'string'
    AND length(message->>'preview_id') BETWEEN 1 AND 512
    AND (message->>'preview' IS DISTINCT FROM 'true' OR idempotency_key IS NULL)
  )
);

-- +goose Down
-- 快照仍作为普通文本消息保留，回滚不撤回已输出的正文。
ALTER TABLE events DROP CONSTRAINT events_reply_preview_check;
DROP INDEX events_reply_preview_key;
UPDATE events SET message=message-'preview_id'-'preview'
WHERE message ? 'preview_id';
