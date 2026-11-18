-- +goose Up
ALTER TABLE card_schedules
    ADD COLUMN archived_at timestamptz;

ALTER TABLE card_schedules
    ADD CONSTRAINT card_schedules_archived_disabled_ck
    CHECK (archived_at IS NULL OR NOT enabled);

DROP INDEX card_schedules_due_idx;
CREATE INDEX card_schedules_due_idx
    ON card_schedules (next_trigger_at)
    WHERE enabled AND archived_at IS NULL;

CREATE INDEX card_schedules_active_card_idx
    ON card_schedules (workspace_id, card_id, created_at, id)
    WHERE archived_at IS NULL;

-- +goose Down
DROP INDEX card_schedules_active_card_idx;
DROP INDEX card_schedules_due_idx;
CREATE INDEX card_schedules_due_idx
    ON card_schedules (next_trigger_at)
    WHERE enabled;
ALTER TABLE card_schedules
    DROP CONSTRAINT card_schedules_archived_disabled_ck;
ALTER TABLE card_schedules
    DROP COLUMN archived_at;
