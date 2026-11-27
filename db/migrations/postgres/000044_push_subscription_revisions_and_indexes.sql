-- +goose Up
-- 按订阅（设备）独立跟踪卡片已推送/已阅版本，彻底解决多设备登录时任一活跃端 ack 吞掉其他空闲设备 Web Push 的问题。
CREATE TABLE push_subscription_card_revisions (
    subscription_id uuid NOT NULL REFERENCES push_subscriptions (id) ON DELETE CASCADE,
    workspace_id uuid NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    card_id uuid NOT NULL,
    revision text NOT NULL,
    event_sequence text DEFAULT '0' NOT NULL,
    message_preview text DEFAULT '' NOT NULL,
    attempt_status text DEFAULT '' NOT NULL,
    title text DEFAULT '' NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    PRIMARY KEY (subscription_id, card_id),
    CONSTRAINT push_subscription_card_revisions_card_fk
        FOREIGN KEY (workspace_id, card_id) REFERENCES cards (workspace_id, id) ON DELETE CASCADE
);

CREATE INDEX push_subscription_card_revisions_workspace_idx
    ON push_subscription_card_revisions (workspace_id, subscription_id);

-- 迁移既有全局基线到每个已有订阅，避免升级瞬间把历史消息重新推送一遍。
INSERT INTO push_subscription_card_revisions (
    subscription_id, workspace_id, card_id, revision, event_sequence, message_preview, attempt_status, title, updated_at
)
SELECT s.id, r.workspace_id, r.card_id, r.revision, r.event_sequence, r.message_preview, r.attempt_status, r.title, r.updated_at
FROM push_subscriptions s
JOIN push_card_revisions r ON r.workspace_id = s.workspace_id
ON CONFLICT (subscription_id, card_id) DO NOTHING;

-- 加速 cardUpdates 查询对每张卡片最新通知事件（正式模型回复与工具待人工确认事件）的 LATERAL 扫描。
CREATE INDEX events_card_notifiable_sequence_idx
    ON events (workspace_id, card_id, sequence DESC)
    WHERE event_type IN ('message', 'tool_approval');

-- 加速 cardUpdates 查询对日程汇总的聚合。
CREATE INDEX card_schedules_card_updated_idx
    ON card_schedules (workspace_id, card_id, updated_at DESC);

-- +goose Down
DROP INDEX IF EXISTS card_schedules_card_updated_idx;
DROP INDEX IF EXISTS events_card_notifiable_sequence_idx;
DROP INDEX IF EXISTS push_subscription_card_revisions_workspace_idx;
DROP TABLE IF EXISTS push_subscription_card_revisions;
