-- 000019_workspace_default_model_routes.up.sql
ALTER TABLE workspace_agent_settings
    ADD COLUMN default_model_route_id UUID REFERENCES workspace_model_routes(id) ON DELETE SET NULL,
    ADD COLUMN discussion_model_route_id UUID REFERENCES workspace_model_routes(id) ON DELETE SET NULL,
    ADD COLUMN plan_model_route_id UUID REFERENCES workspace_model_routes(id) ON DELETE SET NULL;

CREATE INDEX idx_workspace_agent_settings_default_route ON workspace_agent_settings(default_model_route_id);
CREATE INDEX idx_workspace_agent_settings_discussion_route ON workspace_agent_settings(discussion_model_route_id);
CREATE INDEX idx_workspace_agent_settings_plan_route ON workspace_agent_settings(plan_model_route_id);
