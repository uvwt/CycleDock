-- +goose Up
-- 1. workspace_agent_settings 增加工作区全局统一工具守卫配置
ALTER TABLE workspace_agent_settings
    ADD COLUMN tool_guard_mode TEXT DEFAULT 'block_high_risk' NOT NULL,
    ADD COLUMN tool_guard_model_route_id UUID REFERENCES workspace_model_routes(id) ON DELETE SET NULL,
    ADD COLUMN tool_guard_timeout_seconds INTEGER DEFAULT 10 NOT NULL,
    ADD COLUMN tool_guard_custom_rules TEXT DEFAULT '' NOT NULL;

ALTER TABLE workspace_agent_settings
    ADD CONSTRAINT workspace_agent_settings_tool_guard_mode_ck
    CHECK (tool_guard_mode IN ('block_high_risk', 'strict'));

ALTER TABLE workspace_agent_settings
    ADD CONSTRAINT workspace_agent_settings_tool_guard_timeout_ck
    CHECK (tool_guard_timeout_seconds BETWEEN 2 AND 60);

CREATE INDEX idx_workspace_agent_settings_guard_route ON workspace_agent_settings(tool_guard_model_route_id);

-- 2. workspace_device_tool_guards 收敛为纯布尔开关 enabled
ALTER TABLE workspace_device_tool_guards
    ADD COLUMN enabled BOOLEAN DEFAULT false NOT NULL;

-- 3. workspace_mcp_connections 增加纯布尔开关 tool_guard_enabled
ALTER TABLE workspace_mcp_connections
    ADD COLUMN tool_guard_enabled BOOLEAN DEFAULT false NOT NULL;

-- 4. 存量开关状态无损回填
UPDATE workspace_device_tool_guards
SET enabled = (mode IN ('block_high_risk', 'strict'));

UPDATE workspace_mcp_connections
SET tool_guard_enabled = (tool_guard_mode IN ('block_high_risk', 'strict'));

-- 5. 将工作区下存量设备或 MCP 自定义守卫策略择优回填到工作区全局配置
-- 若工作区尚未落库 workspace_agent_settings（极少数只配过守卫未保存过模型路由的场景），
-- 不强行伪造 source_id，仅更新已有 workspace_agent_settings 记录，保证外键与业务语义完整。
WITH scoped_candidates AS (
    SELECT
        workspace_id,
        0 AS source_priority,
        COALESCE(NULLIF(btrim(device_name), ''), device_node_id) AS target_label,
        mode AS guard_mode,
        model_route_id,
        timeout_seconds,
        btrim(custom_rules) AS custom_rules,
        updated_at
    FROM workspace_device_tool_guards
    WHERE mode IN ('block_high_risk', 'strict')
       OR model_route_id IS NOT NULL
       OR timeout_seconds <> 10
       OR btrim(custom_rules) <> ''

    UNION ALL

    SELECT
        workspace_id,
        1 AS source_priority,
        name AS target_label,
        tool_guard_mode AS guard_mode,
        tool_guard_model_route_id AS model_route_id,
        tool_guard_timeout_seconds AS timeout_seconds,
        btrim(tool_guard_custom_rules) AS custom_rules,
        updated_at
    FROM workspace_mcp_connections
    WHERE tool_guard_mode IN ('block_high_risk', 'strict')
       OR tool_guard_model_route_id IS NOT NULL
       OR tool_guard_timeout_seconds <> 10
       OR btrim(tool_guard_custom_rules) <> ''
),
workspace_mode AS (
    SELECT
        workspace_id,
        CASE
            WHEN BOOL_OR(guard_mode = 'strict') THEN 'strict'
            ELSE 'block_high_risk'
        END AS resolved_mode
    FROM scoped_candidates
    GROUP BY workspace_id
),
workspace_route AS (
    SELECT DISTINCT ON (workspace_id)
        workspace_id,
        model_route_id AS resolved_route_id
    FROM scoped_candidates
    WHERE model_route_id IS NOT NULL
    ORDER BY workspace_id,
             (guard_mode IN ('block_high_risk', 'strict')) DESC,
             (guard_mode = 'strict') DESC,
             updated_at DESC,
             source_priority ASC
),
workspace_timeout AS (
    SELECT DISTINCT ON (workspace_id)
        workspace_id,
        LEAST(60, GREATEST(2, timeout_seconds)) AS resolved_timeout
    FROM scoped_candidates
    WHERE timeout_seconds <> 10
    ORDER BY workspace_id,
             (guard_mode IN ('block_high_risk', 'strict')) DESC,
             (guard_mode = 'strict') DESC,
             updated_at DESC,
             source_priority ASC
),
distinct_rules AS (
    SELECT DISTINCT ON (workspace_id, custom_rules)
        workspace_id,
        target_label,
        custom_rules,
        source_priority,
        updated_at
    FROM scoped_candidates
    WHERE custom_rules <> ''
    ORDER BY workspace_id, custom_rules, source_priority ASC, updated_at DESC
),
workspace_rules AS (
    SELECT
        workspace_id,
        CASE
            WHEN COUNT(*) = 1 THEN MAX(custom_rules)
            ELSE STRING_AGG(
                '[' || target_label || '] ' || custom_rules,
                E'\n'
                ORDER BY source_priority ASC, target_label ASC
            )
        END AS resolved_rules
    FROM distinct_rules
    GROUP BY workspace_id
),
aggregated AS (
    SELECT
        m.workspace_id,
        m.resolved_mode,
        r.resolved_route_id,
        COALESCE(t.resolved_timeout, 10) AS resolved_timeout,
        COALESCE(ru.resolved_rules, '') AS resolved_rules
    FROM workspace_mode m
    LEFT JOIN workspace_route r ON r.workspace_id = m.workspace_id
    LEFT JOIN workspace_timeout t ON t.workspace_id = m.workspace_id
    LEFT JOIN workspace_rules ru ON ru.workspace_id = m.workspace_id
)
UPDATE workspace_agent_settings AS s
SET tool_guard_mode = a.resolved_mode,
    tool_guard_model_route_id = a.resolved_route_id,
    tool_guard_timeout_seconds = a.resolved_timeout,
    tool_guard_custom_rules = a.resolved_rules
FROM aggregated a
WHERE s.workspace_id = a.workspace_id;

-- 6. 移除设备表与 MCP 表的冗余独立参数列与约束
ALTER TABLE workspace_device_tool_guards
    DROP CONSTRAINT IF EXISTS workspace_device_tool_guards_mode_ck;
ALTER TABLE workspace_device_tool_guards
    DROP COLUMN IF EXISTS custom_rules,
    DROP COLUMN IF EXISTS timeout_seconds,
    DROP COLUMN IF EXISTS model_route_id,
    DROP COLUMN IF EXISTS mode;

DROP INDEX IF EXISTS idx_workspace_mcp_connections_guard_route;
ALTER TABLE workspace_mcp_connections
    DROP CONSTRAINT IF EXISTS workspace_mcp_connections_guard_mode_ck;
ALTER TABLE workspace_mcp_connections
    DROP COLUMN IF EXISTS tool_guard_custom_rules,
    DROP COLUMN IF EXISTS tool_guard_timeout_seconds,
    DROP COLUMN IF EXISTS tool_guard_model_route_id,
    DROP COLUMN IF EXISTS tool_guard_mode;

-- +goose Down
-- 恢复设备表与 MCP 表的独立配置列
ALTER TABLE workspace_device_tool_guards
    ADD COLUMN mode TEXT DEFAULT 'disabled' NOT NULL,
    ADD COLUMN model_route_id UUID REFERENCES workspace_model_routes(id) ON DELETE SET NULL,
    ADD COLUMN timeout_seconds INTEGER DEFAULT 10 NOT NULL,
    ADD COLUMN custom_rules TEXT DEFAULT '' NOT NULL;

ALTER TABLE workspace_device_tool_guards
    ADD CONSTRAINT workspace_device_tool_guards_mode_ck
    CHECK (mode IN ('disabled', 'block_high_risk', 'strict'));

ALTER TABLE workspace_mcp_connections
    ADD COLUMN tool_guard_mode TEXT DEFAULT 'disabled' NOT NULL,
    ADD COLUMN tool_guard_model_route_id UUID REFERENCES workspace_model_routes(id) ON DELETE SET NULL,
    ADD COLUMN tool_guard_timeout_seconds INTEGER DEFAULT 10 NOT NULL,
    ADD COLUMN tool_guard_custom_rules TEXT DEFAULT '' NOT NULL;

ALTER TABLE workspace_mcp_connections
    ADD CONSTRAINT workspace_mcp_connections_guard_mode_ck
    CHECK (tool_guard_mode IN ('disabled', 'block_high_risk', 'strict'));

CREATE INDEX idx_workspace_mcp_connections_guard_route ON workspace_mcp_connections(tool_guard_model_route_id);

-- 回填 Down 迁移时尽可能依据工作区全局配置还原已开启的设备与 MCP
UPDATE workspace_device_tool_guards g
SET mode = CASE WHEN g.enabled THEN COALESCE(s.tool_guard_mode, 'block_high_risk') ELSE 'disabled' END,
    model_route_id = s.tool_guard_model_route_id,
    timeout_seconds = COALESCE(s.tool_guard_timeout_seconds, 10),
    custom_rules = COALESCE(s.tool_guard_custom_rules, '')
FROM workspace_agent_settings s
WHERE s.workspace_id = g.workspace_id;

UPDATE workspace_device_tool_guards
SET mode = 'block_high_risk'
WHERE enabled AND mode = 'disabled';

UPDATE workspace_mcp_connections m
SET tool_guard_mode = CASE WHEN m.tool_guard_enabled THEN COALESCE(s.tool_guard_mode, 'block_high_risk') ELSE 'disabled' END,
    tool_guard_model_route_id = s.tool_guard_model_route_id,
    tool_guard_timeout_seconds = COALESCE(s.tool_guard_timeout_seconds, 10),
    tool_guard_custom_rules = COALESCE(s.tool_guard_custom_rules, '')
FROM workspace_agent_settings s
WHERE s.workspace_id = m.workspace_id;

UPDATE workspace_mcp_connections
SET tool_guard_mode = 'block_high_risk'
WHERE tool_guard_enabled AND tool_guard_mode = 'disabled';

ALTER TABLE workspace_mcp_connections
    DROP COLUMN IF EXISTS tool_guard_enabled;

ALTER TABLE workspace_device_tool_guards
    DROP COLUMN IF EXISTS enabled;

DROP INDEX IF EXISTS idx_workspace_agent_settings_guard_route;
ALTER TABLE workspace_agent_settings
    DROP CONSTRAINT IF EXISTS workspace_agent_settings_tool_guard_timeout_ck;
ALTER TABLE workspace_agent_settings
    DROP CONSTRAINT IF EXISTS workspace_agent_settings_tool_guard_mode_ck;
ALTER TABLE workspace_agent_settings
    DROP COLUMN IF EXISTS tool_guard_custom_rules,
    DROP COLUMN IF EXISTS tool_guard_timeout_seconds,
    DROP COLUMN IF EXISTS tool_guard_model_route_id,
    DROP COLUMN IF EXISTS tool_guard_mode;
