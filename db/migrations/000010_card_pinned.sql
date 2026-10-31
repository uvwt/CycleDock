-- +goose Up
-- 卡片置顶：讨论或卡片置顶后在列表最上方优先展示。
ALTER TABLE cards ADD COLUMN pinned_at timestamp with time zone;
CREATE INDEX cards_pinned_idx ON cards (workspace_id, project_id, pinned_at DESC NULLS LAST);

-- +goose Down
DROP INDEX IF EXISTS cards_pinned_idx;
ALTER TABLE cards DROP COLUMN IF EXISTS pinned_at;

