-- +goose Up
-- 卡片工具审批记录增加作用域信息及是否允许加入临时白名单标记
ALTER TABLE card_tool_approvals
    ADD COLUMN scope_type TEXT NOT NULL DEFAULT '',
    ADD COLUMN scope_target TEXT NOT NULL DEFAULT '',
    ADD COLUMN scope_label TEXT NOT NULL DEFAULT '',
    ADD COLUMN allow_session_whitelist BOOLEAN NOT NULL DEFAULT true;

ALTER TABLE card_tool_approvals
    ADD CONSTRAINT card_tool_approvals_scope_type_ck
    CHECK (scope_type IN ('', 'device', 'mcp', 'tool'));

-- 卡片级临时白名单改为按（当前卡片 + 设备 / MCP / 兜底工具）精确放行
ALTER TABLE card_tool_whitelists
    ALTER COLUMN tool_name SET DEFAULT '',
    ADD COLUMN scope_type TEXT NOT NULL DEFAULT 'tool',
    ADD COLUMN scope_target TEXT NOT NULL DEFAULT '';

UPDATE card_tool_whitelists
SET scope_type = 'tool',
    scope_target = tool_name
WHERE scope_target = '' AND tool_name <> '';

DELETE FROM card_tool_whitelists a
USING card_tool_whitelists b
WHERE a.ctid < b.ctid
  AND a.workspace_id = b.workspace_id
  AND a.card_id = b.card_id
  AND a.scope_type = b.scope_type
  AND a.scope_target = b.scope_target;

ALTER TABLE card_tool_whitelists
    ADD CONSTRAINT card_tool_whitelists_scope_type_ck
    CHECK (scope_type IN ('device', 'mcp', 'tool'));

DROP INDEX IF EXISTS idx_card_tool_whitelists_card;
CREATE UNIQUE INDEX idx_card_tool_whitelists_scope_uq
    ON card_tool_whitelists(workspace_id, card_id, scope_type, scope_target);

-- +goose Down
DROP INDEX IF EXISTS idx_card_tool_whitelists_scope_uq;
ALTER TABLE card_tool_whitelists DROP CONSTRAINT IF EXISTS card_tool_whitelists_scope_type_ck;
ALTER TABLE card_tool_whitelists
    DROP COLUMN IF EXISTS scope_target,
    DROP COLUMN IF EXISTS scope_type,
    ALTER COLUMN tool_name DROP DEFAULT;
CREATE INDEX idx_card_tool_whitelists_card ON card_tool_whitelists(workspace_id, card_id, tool_name);

ALTER TABLE card_tool_approvals DROP CONSTRAINT IF EXISTS card_tool_approvals_scope_type_ck;
ALTER TABLE card_tool_approvals
    DROP COLUMN IF EXISTS allow_session_whitelist,
    DROP COLUMN IF EXISTS scope_label,
    DROP COLUMN IF EXISTS scope_target,
    DROP COLUMN IF EXISTS scope_type;
