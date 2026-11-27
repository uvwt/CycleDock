-- +goose Up
-- 用户输入时支持选定当前 Turn 的优先模型路由，空值表示使用工作区默认模型顺序。
ALTER TABLE card_turns
  ADD COLUMN preferred_model_route_id uuid REFERENCES workspace_model_routes(id) ON DELETE SET NULL;

-- +goose Down
ALTER TABLE card_turns
  DROP COLUMN preferred_model_route_id;
