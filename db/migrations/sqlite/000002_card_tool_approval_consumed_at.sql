-- +goose Up
-- 为 card_tool_approvals 增加单次审批消费时间 consumed_at：
--   - approve_once 放行凭证只允许原子消费一次（WHERE status = 'approved' AND consumed_at IS NULL），
--     防止历史已审批记录在后续轮次中永久绕过安全守卫。
ALTER TABLE card_tool_approvals
    ADD COLUMN consumed_at TEXT;

-- 历史已审批记录在旧版本中均已触发过执行尝试，升级时统一回填为已消费，避免旧记录被静默复用。
UPDATE card_tool_approvals
SET consumed_at = COALESCE(resolved_at, updated_at, created_at)
WHERE status = 'approved'
  AND consumed_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_card_tool_approvals_unconsumed
    ON card_tool_approvals (workspace_id, card_id, tool_name)
    WHERE status = 'approved' AND consumed_at IS NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_card_tool_approvals_unconsumed;
ALTER TABLE card_tool_approvals
    DROP COLUMN consumed_at;
