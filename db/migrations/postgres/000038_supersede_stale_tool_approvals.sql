-- +goose Up
-- 扩展工具审批状态约束，支持因后续对话或状态推进自动失效（superseded）
ALTER TABLE card_tool_approvals DROP CONSTRAINT IF EXISTS card_tool_approvals_status_ck;
ALTER TABLE card_tool_approvals
    ADD CONSTRAINT card_tool_approvals_status_ck
    CHECK (status IN ('pending', 'approved', 'rejected', 'superseded'));

-- 一次性存量清理：将历史上卡片已结束/暂停或已有更晚对话/轮次推进的僵尸 pending 审批标为 superseded
UPDATE card_tool_approvals ta
SET status = 'superseded',
    resolution_comment = CASE
        WHEN btrim(ta.resolution_comment) = '' THEN '已有后续对话或状态推进，审批已自动失效'
        ELSE ta.resolution_comment
    END,
    resolved_at = COALESCE(ta.resolved_at, now()),
    updated_at = now()
FROM cards c
WHERE c.workspace_id = ta.workspace_id
  AND c.id = ta.card_id
  AND ta.status = 'pending'
  AND (
      (c.kind = 'discussion' AND c.discussion_state = 'archived')
      OR (c.kind = 'todo' AND c.todo_state = 'done')
      OR (c.kind = 'ongoing' AND c.ongoing_state IN ('paused', 'archived'))
      OR (
          ta.turn_id IS NOT NULL
          AND (
              NOT EXISTS (
                  SELECT 1
                  FROM card_turns origin_turn
                  WHERE origin_turn.workspace_id = ta.workspace_id
                    AND origin_turn.card_id = ta.card_id
                    AND origin_turn.id = ta.turn_id
                    AND origin_turn.cancelled_at IS NULL
              )
              OR EXISTS (
                  SELECT 1
                  FROM card_turns origin_turn
                  JOIN card_turns newer
                    ON newer.workspace_id = ta.workspace_id
                   AND newer.card_id = ta.card_id
                   AND newer.cancelled_at IS NULL
                   AND newer.sequence > origin_turn.sequence
                  WHERE origin_turn.workspace_id = ta.workspace_id
                    AND origin_turn.card_id = ta.card_id
                    AND origin_turn.id = ta.turn_id
              )
          )
      )
      OR (
          ta.turn_id IS NULL
          AND (
              EXISTS (
                  SELECT 1
                  FROM card_turns newer
                  WHERE newer.workspace_id = ta.workspace_id
                    AND newer.card_id = ta.card_id
                    AND newer.cancelled_at IS NULL
                    AND newer.created_at > ta.created_at
              )
              OR EXISTS (
                  SELECT 1
                  FROM events newer_ev
                  WHERE newer_ev.workspace_id = ta.workspace_id
                    AND newer_ev.card_id = ta.card_id
                    AND newer_ev.event_type = 'comment'
                    AND newer_ev.created_at > ta.created_at
              )
          )
      )
  );

-- +goose Down
UPDATE card_tool_approvals
SET status = 'rejected',
    resolution_comment = CASE
        WHEN btrim(resolution_comment) = '' THEN '审批已随版本回滚标记为失效'
        ELSE resolution_comment
    END,
    resolved_at = COALESCE(resolved_at, now()),
    updated_at = now()
WHERE status = 'superseded';

ALTER TABLE card_tool_approvals DROP CONSTRAINT IF EXISTS card_tool_approvals_status_ck;
ALTER TABLE card_tool_approvals
    ADD CONSTRAINT card_tool_approvals_status_ck
    CHECK (status IN ('pending', 'approved', 'rejected'));
