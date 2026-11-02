-- 000019_workspace_default_model_routes.down.sql
DROP INDEX IF EXISTS idx_workspace_agent_settings_plan_route;
DROP INDEX IF EXISTS idx_workspace_agent_settings_discussion_route;
DROP INDEX IF EXISTS idx_workspace_agent_settings_default_route;

ALTER TABLE workspace_agent_settings
    DROP COLUMN IF EXISTS plan_model_route_id,
    DROP COLUMN IF EXISTS discussion_model_route_id,
    DROP COLUMN IF EXISTS default_model_route_id;
