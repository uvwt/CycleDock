-- +goose Up
-- 关闭页面后的卡片通知走 Web Push。一个浏览器 endpoint 只属于当前 Workspace。
-- active_until 未过期表示页面还活着，由打开的页面自己通知，不再推送。
-- Safari 收到推送却不展示时会收回该网站的通知权限。
CREATE TABLE push_subscriptions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id uuid NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES human_accounts (id) ON DELETE CASCADE,
    endpoint text NOT NULL,
    p256dh text NOT NULL,
    auth_secret text NOT NULL,
    active_until timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT push_subscriptions_endpoint_key UNIQUE (endpoint),
    CONSTRAINT push_subscriptions_endpoint_ck CHECK (char_length(endpoint) BETWEEN 8 AND 2000 AND endpoint LIKE 'https://%'),
    CONSTRAINT push_subscriptions_p256dh_ck CHECK (char_length(p256dh) BETWEEN 1 AND 200),
    CONSTRAINT push_subscriptions_auth_ck CHECK (char_length(auth_secret) BETWEEN 1 AND 200)
);

CREATE INDEX push_subscriptions_workspace_idx ON push_subscriptions (workspace_id);

-- 已经观察过的卡片版本。没有记录时只建立基线，不把历史更新补推一遍。
CREATE TABLE push_card_revisions (
    workspace_id uuid NOT NULL,
    card_id uuid NOT NULL,
    revision text NOT NULL,
    event_sequence text DEFAULT '0' NOT NULL,
    message_preview text DEFAULT '' NOT NULL,
    attempt_status text DEFAULT '' NOT NULL,
    title text DEFAULT '' NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    PRIMARY KEY (workspace_id, card_id),
    CONSTRAINT push_card_revisions_workspace_fk FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE,
    CONSTRAINT push_card_revisions_card_fk FOREIGN KEY (workspace_id, card_id) REFERENCES cards (workspace_id, id) ON DELETE CASCADE
);

-- +goose Down
DROP TABLE push_card_revisions;
DROP TABLE push_subscriptions;
