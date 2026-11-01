-- +goose Up
ALTER TABLE cards ADD COLUMN confirmation_round bigint NOT NULL DEFAULT 0
    CHECK (confirmation_round >= 0);
ALTER TABLE events ADD COLUMN confirmation_round bigint NOT NULL DEFAULT 0
    CHECK (confirmation_round >= 0);

-- 保留历史确认，并按已有 Working 状态事件还原轮次；已重新开工的卡片不复用旧确认。
UPDATE events confirmation
SET confirmation_round = (
    SELECT count(*) FROM events transition
    WHERE transition.workspace_id = confirmation.workspace_id
      AND transition.card_id = confirmation.card_id
      AND transition.sequence < confirmation.sequence
      AND transition.event_type = 'status_change'
      AND transition.content IN ('todo -> working', 'review -> working', 'done -> working')
)
WHERE confirmation.event_type = 'confirm';
UPDATE cards c
SET confirmation_round = (
    SELECT count(*) FROM events transition
    WHERE transition.workspace_id = c.workspace_id
      AND transition.card_id = c.id
      AND transition.event_type = 'status_change'
      AND transition.content IN ('todo -> working', 'review -> working', 'done -> working')
)
WHERE c.kind = 'todo';

DROP INDEX events_confirm_source_uq;
CREATE UNIQUE INDEX events_confirm_source_uq
    ON events (workspace_id, card_id, actor_source_id, confirmation_round)
    WHERE event_type = 'confirm';

-- +goose Down
-- 若已有多轮确认，旧唯一约束会拒绝回退，避免静默删除历史证据。
DROP INDEX events_confirm_source_uq;
CREATE UNIQUE INDEX events_confirm_source_uq
    ON events (workspace_id, card_id, actor_source_id) WHERE event_type = 'confirm';
ALTER TABLE events DROP COLUMN confirmation_round;
ALTER TABLE cards DROP COLUMN confirmation_round;
