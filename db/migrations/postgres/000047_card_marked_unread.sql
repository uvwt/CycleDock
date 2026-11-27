-- +goose Up
ALTER TABLE card_read_cursors
    ADD COLUMN marked_unread boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE card_read_cursors
    DROP COLUMN IF EXISTS marked_unread;
