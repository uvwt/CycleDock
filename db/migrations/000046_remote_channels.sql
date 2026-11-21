-- +goose Up
-- 远程通道配置表：支持 6 大 IM 平台（微信、企业微信、飞书、钉钉、QQ、Telegram），
-- 区分「项目主聊天双向同步（main_chat）」与「定时任务独立推送（scheduled_push）」两种角色。
-- 敏感凭据统一经服务端 AES-256-GCM 加密后写入 secret_ciphertext，绝不明文落库。
CREATE TABLE remote_channels (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    name text NOT NULL CHECK (btrim(name) <> ''::text),
    platform text NOT NULL CHECK (platform = ANY (ARRAY[
        'weixin'::text, 'wecom'::text, 'feishu'::text, 'dingtalk'::text, 'qq'::text, 'telegram'::text
    ])),
    role text NOT NULL CHECK (role = ANY (ARRAY['main_chat'::text, 'scheduled_push'::text])),
    transport text NOT NULL CHECK (transport = ANY (ARRAY[
        'long_poll'::text, 'websocket'::text, 'stream'::text, 'webhook'::text
    ])),
    enabled boolean NOT NULL DEFAULT true,
    project_id uuid NOT NULL,
    target_card_id uuid,
    push_filter text NOT NULL DEFAULT 'all'::text CHECK (push_filter = ANY (ARRAY['all'::text, 'warnings_only'::text])),
    mirror_web_messages boolean NOT NULL DEFAULT false,
    group_policy text NOT NULL DEFAULT 'mention_only'::text CHECK (group_policy = ANY (ARRAY[
        'mention_only'::text, 'allowlist'::text, 'all'::text
    ])),
    allow_from text[] NOT NULL DEFAULT '{}'::text[],
    config_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    secret_ciphertext bytea,
    secret_hints jsonb NOT NULL DEFAULT '{}'::jsonb,
    runtime_status text NOT NULL DEFAULT 'disconnected'::text CHECK (runtime_status = ANY (ARRAY[
        'disconnected'::text, 'connecting'::text, 'connected'::text, 'error'::text
    ])),
    last_error text NOT NULL DEFAULT ''::text,
    last_heartbeat_at timestamp with time zone,
    last_inbound_at timestamp with time zone,
    last_outbound_at timestamp with time zone,
    runtime_state_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_by_source_id uuid,
    created_at timestamp with time zone NOT NULL DEFAULT now(),
    updated_at timestamp with time zone NOT NULL DEFAULT now(),
    CONSTRAINT remote_channels_workspace_id_id_key UNIQUE (workspace_id, id),
    CONSTRAINT remote_channels_platform_transport_ck CHECK (
        (platform = 'weixin'::text AND transport = 'long_poll'::text)
        OR (platform = 'wecom'::text AND transport = ANY (ARRAY['websocket'::text, 'webhook'::text]))
        OR (platform = 'feishu'::text AND transport = ANY (ARRAY['websocket'::text, 'webhook'::text]))
        OR (platform = 'dingtalk'::text AND transport = ANY (ARRAY['stream'::text, 'webhook'::text]))
        OR (platform = 'qq'::text AND transport = 'websocket'::text)
        OR (platform = 'telegram'::text AND transport = 'long_poll'::text)
    ),
    CONSTRAINT remote_channels_role_shape_ck CHECK (
        (role = 'main_chat'::text AND target_card_id IS NULL AND transport <> 'webhook'::text)
        OR (role = 'scheduled_push'::text)
    ),
    CONSTRAINT remote_channels_project_fk FOREIGN KEY (workspace_id, project_id)
        REFERENCES projects(workspace_id, id) ON DELETE CASCADE,
    CONSTRAINT remote_channels_card_fk FOREIGN KEY (workspace_id, target_card_id)
        REFERENCES cards(workspace_id, id) ON DELETE SET NULL,
    CONSTRAINT remote_channels_creator_fk FOREIGN KEY (workspace_id, created_by_source_id)
        REFERENCES sources(workspace_id, id) ON DELETE SET NULL
);

CREATE INDEX remote_channels_workspace_idx
    ON remote_channels (workspace_id, created_at DESC, id);

CREATE INDEX remote_channels_enabled_idx
    ON remote_channels (workspace_id, role, project_id)
    WHERE enabled = true;

-- 入站消息幂等去重表：防止长连接重连或平台重投导致重复写入主聊天 Turn；
-- 同时记录产生的 event_id 与 turn_id，用于出站防回环和来源判定。
CREATE TABLE remote_channel_inbound_dedup (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL,
    channel_id uuid NOT NULL,
    external_message_id text NOT NULL CHECK (btrim(external_message_id) <> ''::text),
    sender_id text NOT NULL DEFAULT ''::text,
    chat_id text NOT NULL DEFAULT ''::text,
    card_id uuid,
    event_id uuid,
    turn_id uuid,
    received_at timestamp with time zone NOT NULL DEFAULT now(),
    CONSTRAINT remote_channel_inbound_dedup_uq UNIQUE (workspace_id, channel_id, external_message_id),
    CONSTRAINT remote_channel_inbound_dedup_channel_fk FOREIGN KEY (workspace_id, channel_id)
        REFERENCES remote_channels(workspace_id, id) ON DELETE CASCADE
);

CREATE INDEX remote_channel_inbound_dedup_event_idx
    ON remote_channel_inbound_dedup (workspace_id, event_id)
    WHERE event_id IS NOT NULL;

CREATE INDEX remote_channel_inbound_dedup_turn_idx
    ON remote_channel_inbound_dedup (workspace_id, turn_id)
    WHERE turn_id IS NOT NULL;

CREATE INDEX remote_channel_inbound_dedup_received_idx
    ON remote_channel_inbound_dedup (workspace_id, channel_id, received_at DESC);

-- 出站投递水位游标表：按通道+卡片记录已同步的最新终态消息、评论、巡检、状态和审批水位，
-- 确保进程重启不重放历史消息，也避免不同通道之间互相干扰。
CREATE TABLE remote_channel_outbound_cursors (
    workspace_id uuid NOT NULL,
    channel_id uuid NOT NULL,
    card_id uuid NOT NULL,
    last_message_seq bigint NOT NULL DEFAULT 0,
    last_comment_seq bigint NOT NULL DEFAULT 0,
    last_checkin_seq bigint NOT NULL DEFAULT 0,
    last_status_seq bigint NOT NULL DEFAULT 0,
    last_attempt_id uuid,
    last_attempt_status text NOT NULL DEFAULT ''::text,
    last_approval_id uuid,
    updated_at timestamp with time zone NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, channel_id, card_id),
    CONSTRAINT remote_channel_outbound_cursors_channel_fk FOREIGN KEY (workspace_id, channel_id)
        REFERENCES remote_channels(workspace_id, id) ON DELETE CASCADE,
    CONSTRAINT remote_channel_outbound_cursors_card_fk FOREIGN KEY (workspace_id, card_id)
        REFERENCES cards(workspace_id, id) ON DELETE CASCADE
);

-- +goose Down
DROP TABLE IF EXISTS remote_channel_outbound_cursors;
DROP TABLE IF EXISTS remote_channel_inbound_dedup;
DROP TABLE IF EXISTS remote_channels;
