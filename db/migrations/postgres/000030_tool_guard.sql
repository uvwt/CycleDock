-- +goose Up
-- 工具调用安全守卫（Tool Guard）工作区配置
ALTER TABLE workspace_agent_settings
    ADD COLUMN tool_guard_mode TEXT DEFAULT 'disabled' NOT NULL,
    ADD COLUMN tool_guard_model_route_id UUID REFERENCES workspace_model_routes(id) ON DELETE SET NULL,
    ADD COLUMN tool_guard_timeout_seconds INTEGER DEFAULT 10 NOT NULL,
    ADD COLUMN tool_guard_custom_rules TEXT DEFAULT '' NOT NULL;

ALTER TABLE workspace_agent_settings
    ADD CONSTRAINT workspace_agent_settings_tool_guard_mode_ck
    CHECK (tool_guard_mode IN ('disabled', 'block_high_risk', 'strict'));

CREATE INDEX idx_workspace_agent_settings_guard_route ON workspace_agent_settings(tool_guard_model_route_id);

-- 工具审批持久化表
CREATE TABLE card_tool_approvals (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    card_id UUID NOT NULL,
    turn_id UUID,
    attempt_id UUID,
    tool_call_id TEXT NOT NULL DEFAULT '',
    tool_name TEXT NOT NULL,
    arguments JSONB NOT NULL DEFAULT '{}'::jsonb,
    risk_level TEXT NOT NULL,
    reversible BOOLEAN NOT NULL DEFAULT false,
    reason TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending',
    resolution_comment TEXT NOT NULL DEFAULT '',
    resolved_by_source_id UUID,
    resolved_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
    CONSTRAINT card_tool_approvals_card_fk FOREIGN KEY (workspace_id, card_id) REFERENCES cards(workspace_id, id) ON DELETE CASCADE,
    CONSTRAINT card_tool_approvals_source_fk FOREIGN KEY (workspace_id, resolved_by_source_id) REFERENCES sources(workspace_id, id) ON DELETE SET NULL,
    CONSTRAINT card_tool_approvals_status_ck CHECK (status IN ('pending', 'approved', 'rejected')),
    CONSTRAINT card_tool_approvals_risk_ck CHECK (risk_level IN ('low', 'medium', 'high', 'critical'))
);

CREATE INDEX idx_card_tool_approvals_card_status ON card_tool_approvals(workspace_id, card_id, status);

-- 卡片级临时放行白名单表（支持“本卡片同类操作不再询问”）
CREATE TABLE card_tool_whitelists (
    id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    card_id UUID NOT NULL,
    tool_name TEXT NOT NULL,
    pattern TEXT NOT NULL DEFAULT '*',
    created_by_source_id UUID,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
    CONSTRAINT card_tool_whitelists_card_fk FOREIGN KEY (workspace_id, card_id) REFERENCES cards(workspace_id, id) ON DELETE CASCADE,
    CONSTRAINT card_tool_whitelists_source_fk FOREIGN KEY (workspace_id, created_by_source_id) REFERENCES sources(workspace_id, id) ON DELETE SET NULL
);

CREATE INDEX idx_card_tool_whitelists_card ON card_tool_whitelists(workspace_id, card_id, tool_name);

-- events 表增加 tool_approval 关联与事件类型
ALTER TABLE events ADD COLUMN tool_approval_id UUID REFERENCES card_tool_approvals(id) ON DELETE SET NULL;
ALTER TABLE events DROP CONSTRAINT events_event_type_check;
ALTER TABLE events ADD CONSTRAINT events_event_type_check CHECK (event_type IN
  ('comment','progress','finding','result','checkin','confirm','reject','status_change','message','checkin_ack','tool_approval'));

-- +goose Down
DELETE FROM events WHERE event_type = 'tool_approval';
ALTER TABLE events DROP CONSTRAINT events_event_type_check;
ALTER TABLE events ADD CONSTRAINT events_event_type_check CHECK (event_type IN
  ('comment','progress','finding','result','checkin','confirm','reject','status_change','message','checkin_ack'));
ALTER TABLE events DROP COLUMN IF EXISTS tool_approval_id;

DROP TABLE IF EXISTS card_tool_whitelists;
DROP TABLE IF EXISTS card_tool_approvals;

DROP INDEX IF EXISTS idx_workspace_agent_settings_guard_route;
ALTER TABLE workspace_agent_settings DROP CONSTRAINT IF EXISTS workspace_agent_settings_tool_guard_mode_ck;
ALTER TABLE workspace_agent_settings
    DROP COLUMN IF EXISTS tool_guard_custom_rules,
    DROP COLUMN IF EXISTS tool_guard_timeout_seconds,
    DROP COLUMN IF EXISTS tool_guard_model_route_id,
    DROP COLUMN IF EXISTS tool_guard_mode;
