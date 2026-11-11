-- +goose Up
-- 设备级 Tool Guard 安全守卫配置表
CREATE TABLE workspace_device_tool_guards (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    device_node_id TEXT NOT NULL,
    device_name TEXT NOT NULL DEFAULT '',
    mode TEXT NOT NULL DEFAULT 'disabled',
    model_route_id UUID REFERENCES workspace_model_routes(id) ON DELETE SET NULL,
    timeout_seconds INTEGER NOT NULL DEFAULT 10,
    custom_rules TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
    CONSTRAINT workspace_device_tool_guards_mode_ck CHECK (mode IN ('disabled', 'block_high_risk', 'strict')),
    CONSTRAINT workspace_device_tool_guards_node_uq UNIQUE (workspace_id, device_node_id)
);

CREATE INDEX idx_workspace_device_tool_guards_workspace ON workspace_device_tool_guards(workspace_id);

-- MCP 连接表增加独立的 Tool Guard 配置字段
ALTER TABLE workspace_mcp_connections
    ADD COLUMN tool_guard_mode TEXT DEFAULT 'disabled' NOT NULL,
    ADD COLUMN tool_guard_model_route_id UUID REFERENCES workspace_model_routes(id) ON DELETE SET NULL,
    ADD COLUMN tool_guard_timeout_seconds INTEGER DEFAULT 10 NOT NULL,
    ADD COLUMN tool_guard_custom_rules TEXT DEFAULT '' NOT NULL;

ALTER TABLE workspace_mcp_connections
    ADD CONSTRAINT workspace_mcp_connections_guard_mode_ck
    CHECK (tool_guard_mode IN ('disabled', 'block_high_risk', 'strict'));

CREATE INDEX idx_workspace_mcp_connections_guard_route ON workspace_mcp_connections(tool_guard_model_route_id);

-- 从 workspace_agent_settings 中移除全局 tool_guard 配置
DROP INDEX IF EXISTS idx_workspace_agent_settings_guard_route;
ALTER TABLE workspace_agent_settings DROP CONSTRAINT IF EXISTS workspace_agent_settings_tool_guard_mode_ck;
ALTER TABLE workspace_agent_settings
    DROP COLUMN IF EXISTS tool_guard_custom_rules,
    DROP COLUMN IF EXISTS tool_guard_timeout_seconds,
    DROP COLUMN IF EXISTS tool_guard_model_route_id,
    DROP COLUMN IF EXISTS tool_guard_mode;

-- +goose Down
ALTER TABLE workspace_agent_settings
    ADD COLUMN tool_guard_mode TEXT DEFAULT 'disabled' NOT NULL,
    ADD COLUMN tool_guard_model_route_id UUID REFERENCES workspace_model_routes(id) ON DELETE SET NULL,
    ADD COLUMN tool_guard_timeout_seconds INTEGER DEFAULT 10 NOT NULL,
    ADD COLUMN tool_guard_custom_rules TEXT DEFAULT '' NOT NULL;

ALTER TABLE workspace_agent_settings
    ADD CONSTRAINT workspace_agent_settings_tool_guard_mode_ck
    CHECK (tool_guard_mode IN ('disabled', 'block_high_risk', 'strict'));

CREATE INDEX idx_workspace_agent_settings_guard_route ON workspace_agent_settings(tool_guard_model_route_id);

DROP INDEX IF EXISTS idx_workspace_mcp_connections_guard_route;
ALTER TABLE workspace_mcp_connections DROP CONSTRAINT IF EXISTS workspace_mcp_connections_guard_mode_ck;
ALTER TABLE workspace_mcp_connections
    DROP COLUMN IF EXISTS tool_guard_custom_rules,
    DROP COLUMN IF EXISTS tool_guard_timeout_seconds,
    DROP COLUMN IF EXISTS tool_guard_model_route_id,
    DROP COLUMN IF EXISTS tool_guard_mode;

DROP TABLE IF EXISTS workspace_device_tool_guards;
