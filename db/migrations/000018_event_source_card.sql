-- +goose Up
ALTER TABLE events ADD COLUMN source_card_id uuid;
ALTER TABLE events ADD CONSTRAINT events_source_card_fk
    FOREIGN KEY (workspace_id, source_card_id) REFERENCES cards(workspace_id, id) ON DELETE SET NULL;

-- +goose Down
ALTER TABLE events DROP CONSTRAINT events_source_card_fk;
ALTER TABLE events DROP COLUMN source_card_id;

