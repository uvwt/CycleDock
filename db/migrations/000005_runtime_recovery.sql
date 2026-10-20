-- +goose Up

ALTER TABLE triggers
    ADD COLUMN retry_of_trigger_id uuid NULL,
    ADD COLUMN cancelled_at timestamptz NULL,
    ADD COLUMN cancelled_by_source_id uuid NULL,
    ADD COLUMN cancel_reason text NOT NULL DEFAULT '',
    ADD CONSTRAINT triggers_retry_of_fk
        FOREIGN KEY (workspace_id, card_id, retry_of_trigger_id)
        REFERENCES triggers(workspace_id, card_id, id)
        ON DELETE RESTRICT,
    ADD CONSTRAINT triggers_cancelled_by_fk
        FOREIGN KEY (workspace_id, cancelled_by_source_id)
        REFERENCES sources(workspace_id, id),
    ADD CONSTRAINT triggers_retry_shape_ck CHECK (
        (cause = 'manual_retry' AND retry_of_trigger_id IS NOT NULL)
        OR
        (cause = 'system_followup')
        OR
        (cause NOT IN ('manual_retry', 'system_followup') AND retry_of_trigger_id IS NULL)
    ),
    ADD CONSTRAINT triggers_cancel_shape_ck CHECK (
        (cancelled_at IS NULL AND cancelled_by_source_id IS NULL AND cancel_reason = '')
        OR
        (cancelled_at IS NOT NULL AND cancelled_by_source_id IS NOT NULL)
    );

CREATE UNIQUE INDEX triggers_retry_once_uq
    ON triggers(retry_of_trigger_id)
    WHERE retry_of_trigger_id IS NOT NULL;

CREATE INDEX triggers_active_pending_idx
    ON triggers(workspace_id, card_id, sequence)
    WHERE cancelled_at IS NULL;

ALTER TABLE agent_runs
    ADD COLUMN cancelled_by_source_id uuid NULL,
    ADD COLUMN cancel_reason text NOT NULL DEFAULT '',
    ADD CONSTRAINT agent_runs_cancelled_by_fk
        FOREIGN KEY (workspace_id, cancelled_by_source_id)
        REFERENCES sources(workspace_id, id),
    ADD CONSTRAINT agent_runs_cancel_shape_ck CHECK (
        (cancelled_by_source_id IS NULL AND cancel_reason = '')
        OR
        (status = 'cancelled' AND cancelled_by_source_id IS NOT NULL)
    );

-- +goose Down

ALTER TABLE agent_runs
    DROP CONSTRAINT IF EXISTS agent_runs_cancel_shape_ck,
    DROP CONSTRAINT IF EXISTS agent_runs_cancelled_by_fk,
    DROP COLUMN IF EXISTS cancel_reason,
    DROP COLUMN IF EXISTS cancelled_by_source_id;

DROP INDEX IF EXISTS triggers_active_pending_idx;
DROP INDEX IF EXISTS triggers_retry_once_uq;

ALTER TABLE triggers
    DROP CONSTRAINT IF EXISTS triggers_cancel_shape_ck,
    DROP CONSTRAINT IF EXISTS triggers_cancelled_by_fk,
    DROP CONSTRAINT IF EXISTS triggers_retry_shape_ck,
    DROP CONSTRAINT IF EXISTS triggers_retry_of_fk,
    DROP COLUMN IF EXISTS cancel_reason,
    DROP COLUMN IF EXISTS cancelled_by_source_id,
    DROP COLUMN IF EXISTS cancelled_at,
    DROP COLUMN IF EXISTS retry_of_trigger_id;
