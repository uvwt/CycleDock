-- +goose Up
-- 提问结束本轮执行；已有等待记录收口为完成，不再占用 current Attempt。
ALTER TABLE turn_attempts DROP CONSTRAINT turn_attempts_lifecycle_ck;
ALTER TABLE turn_attempts DROP CONSTRAINT turn_attempts_status_check;
UPDATE turn_attempts
SET status='completed', finished_at=updated_at, heartbeat_at=NULL, lease_expires_at=NULL,
    current_stage='', progress_summary=''
WHERE status='waiting_human';
ALTER TABLE turn_attempts ADD CONSTRAINT turn_attempts_status_check
    CHECK (status IN ('queued','running','completed','failed','superseded','expired','cancelled'));
ALTER TABLE turn_attempts ADD CONSTRAINT turn_attempts_lifecycle_ck CHECK (
    (status='queued' AND started_at IS NULL AND heartbeat_at IS NULL AND lease_expires_at IS NULL AND finished_at IS NULL)
    OR (status='running' AND started_at IS NOT NULL AND heartbeat_at IS NOT NULL AND lease_expires_at IS NOT NULL AND finished_at IS NULL)
    OR (status IN ('completed','failed','superseded','expired','cancelled') AND finished_at IS NOT NULL AND heartbeat_at IS NULL AND lease_expires_at IS NULL)
);
DROP INDEX turn_attempts_one_current_per_card_uq;
CREATE UNIQUE INDEX turn_attempts_one_current_per_card_uq ON turn_attempts (workspace_id, card_id)
    WHERE status IN ('queued','running');

-- +goose Down
ALTER TABLE turn_attempts DROP CONSTRAINT turn_attempts_lifecycle_ck;
ALTER TABLE turn_attempts DROP CONSTRAINT turn_attempts_status_check;
ALTER TABLE turn_attempts ADD CONSTRAINT turn_attempts_status_check
    CHECK (status IN ('queued','running','waiting_human','completed','failed','superseded','expired','cancelled'));
ALTER TABLE turn_attempts ADD CONSTRAINT turn_attempts_lifecycle_ck CHECK (
    (status='queued' AND started_at IS NULL AND heartbeat_at IS NULL AND lease_expires_at IS NULL AND finished_at IS NULL)
    OR (status='running' AND started_at IS NOT NULL AND heartbeat_at IS NOT NULL AND lease_expires_at IS NOT NULL AND finished_at IS NULL)
    OR (status='waiting_human' AND started_at IS NOT NULL AND heartbeat_at IS NULL AND lease_expires_at IS NULL AND finished_at IS NULL)
    OR (status IN ('completed','failed','superseded','expired','cancelled') AND finished_at IS NOT NULL AND heartbeat_at IS NULL AND lease_expires_at IS NULL)
);
DROP INDEX turn_attempts_one_current_per_card_uq;
CREATE UNIQUE INDEX turn_attempts_one_current_per_card_uq ON turn_attempts (workspace_id, card_id)
    WHERE status IN ('queued','running','waiting_human');
