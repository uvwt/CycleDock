-- +goose Up
-- 工作区默认模型路由（看板、讨论、定时任务三个维度）
ALTER TABLE workspace_agent_settings
    ADD COLUMN default_model_route_id UUID REFERENCES workspace_model_routes(id) ON DELETE SET NULL,
    ADD COLUMN discussion_model_route_id UUID REFERENCES workspace_model_routes(id) ON DELETE SET NULL,
    ADD COLUMN plan_model_route_id UUID REFERENCES workspace_model_routes(id) ON DELETE SET NULL;

CREATE INDEX idx_workspace_agent_settings_default_route ON workspace_agent_settings(default_model_route_id);
CREATE INDEX idx_workspace_agent_settings_discussion_route ON workspace_agent_settings(discussion_model_route_id);
CREATE INDEX idx_workspace_agent_settings_plan_route ON workspace_agent_settings(plan_model_route_id);

-- +goose Down
DROP INDEX IF EXISTS idx_workspace_agent_settings_plan_route;
DROP INDEX IF EXISTS idx_workspace_agent_settings_discussion_route;
DROP INDEX IF EXISTS idx_workspace_agent_settings_default_route;

ALTER TABLE workspace_agent_settings
    DROP COLUMN IF EXISTS plan_model_route_id,
    DROP COLUMN IF EXISTS discussion_model_route_id,
    DROP COLUMN IF EXISTS default_model_route_id;
