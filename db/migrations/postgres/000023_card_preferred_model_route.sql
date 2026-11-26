-- +goose Up
-- 卡片（持久会话）支持绑定优先模型路由，空值表示跟随工作区/卡片类型默认模型。
ALTER TABLE cards
  ADD COLUMN preferred_model_route_id uuid REFERENCES workspace_model_routes(id) ON DELETE SET NULL;

CREATE INDEX idx_cards_preferred_model_route ON cards(preferred_model_route_id);

-- +goose Down
DROP INDEX IF EXISTS idx_cards_preferred_model_route;

ALTER TABLE cards
  DROP COLUMN IF EXISTS preferred_model_route_id;
