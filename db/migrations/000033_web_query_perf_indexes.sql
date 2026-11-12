-- +goose Up
-- 加速卡片列表、通知轮询与 SSE 流对每张卡片最新 Attempt 的 LATERAL 查询
CREATE INDEX turn_attempts_card_latest_idx
    ON turn_attempts (workspace_id, card_id, created_at DESC, attempt_number DESC, id DESC);

-- 加速卡片轮次最新序号聚合与列表判断
CREATE INDEX card_turns_card_sequence_idx
    ON card_turns (workspace_id, card_id, sequence DESC);

-- 加速时间线过滤已取消排队轮次对应的原始消息事件
CREATE INDEX card_turns_cancelled_source_event_idx
    ON card_turns (workspace_id, card_id, source_event_id)
    WHERE cancelled_at IS NOT NULL AND source_event_id IS NOT NULL;

-- +goose Down
DROP INDEX card_turns_cancelled_source_event_idx;
DROP INDEX card_turns_card_sequence_idx;
DROP INDEX turn_attempts_card_latest_idx;
