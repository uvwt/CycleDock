-- +goose Up
-- 加速卡片列表与通知流 LATERAL 子查询过滤最新可见消息（comment, message）
CREATE INDEX events_card_visible_messages_idx
    ON events (workspace_id, card_id, sequence DESC)
    WHERE event_type IN ('comment', 'message');

-- +goose Down
DROP INDEX events_card_visible_messages_idx;
