-- +goose Up
-- 卡片级消息推送通知静音开关，默认开启推送（false）
ALTER TABLE cards
    ADD COLUMN notifications_muted BOOLEAN NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE cards
    DROP COLUMN IF EXISTS notifications_muted;
