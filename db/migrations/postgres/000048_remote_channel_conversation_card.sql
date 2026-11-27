-- +goose Up
-- 为双向远程通道增加专属普通讨论卡绑定列（允许为空，按需懒创建；若卡片被删除则自动置空并由入站逻辑重建）。
ALTER TABLE remote_channels
  ADD COLUMN IF NOT EXISTS conversation_card_id uuid;

ALTER TABLE remote_channels
  DROP CONSTRAINT IF EXISTS remote_channels_conversation_card_fk;

ALTER TABLE remote_channels
  ADD CONSTRAINT remote_channels_conversation_card_fk
  FOREIGN KEY (conversation_card_id) REFERENCES cards(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS remote_channels_conversation_card_idx
  ON remote_channels (workspace_id, conversation_card_id)
  WHERE conversation_card_id IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS remote_channels_conversation_card_idx;
ALTER TABLE remote_channels DROP CONSTRAINT IF EXISTS remote_channels_conversation_card_fk;
ALTER TABLE remote_channels DROP COLUMN IF EXISTS conversation_card_id;
