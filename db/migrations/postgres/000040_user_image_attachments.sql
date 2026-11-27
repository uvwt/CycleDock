-- +goose Up
-- 仅持久化本地临时图片附件的轻量元数据与磁盘路径引用，严禁将图片二进制或 base64 写入 PostgreSQL。
CREATE TABLE workspace_attachments (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    card_id uuid,
    uploaded_by_source_id uuid NOT NULL,
    mime_type text NOT NULL,
    size_bytes bigint NOT NULL CHECK (size_bytes > 0),
    width integer NOT NULL CHECK (width > 0),
    height integer NOT NULL CHECK (height > 0),
    sha256 text NOT NULL CHECK (btrim(sha256) <> ''),
    relative_path text NOT NULL CHECK (btrim(relative_path) <> ''),
    created_at timestamp with time zone NOT NULL DEFAULT now(),
    expires_at timestamp with time zone NOT NULL,
    purged_at timestamp with time zone,
    CONSTRAINT workspace_attachments_card_fk
        FOREIGN KEY (workspace_id, card_id)
        REFERENCES cards(workspace_id, id)
        ON DELETE SET NULL,
    CONSTRAINT workspace_attachments_source_fk
        FOREIGN KEY (workspace_id, uploaded_by_source_id)
        REFERENCES sources(workspace_id, id)
        ON DELETE RESTRICT
);

CREATE INDEX workspace_attachments_active_expires_idx
    ON workspace_attachments (expires_at ASC, id ASC)
    WHERE purged_at IS NULL;

CREATE INDEX workspace_attachments_active_created_idx
    ON workspace_attachments (created_at ASC, id ASC)
    WHERE purged_at IS NULL;

CREATE INDEX workspace_attachments_ws_sha256_idx
    ON workspace_attachments (workspace_id, sha256)
    WHERE purged_at IS NULL;

CREATE INDEX workspace_attachments_ws_card_idx
    ON workspace_attachments (workspace_id, card_id)
    WHERE card_id IS NOT NULL;

-- 允许人类 comment 事件在 events.message 中附带轻量图片引用元数据（依然严禁二进制）。
ALTER TABLE events DROP CONSTRAINT events_message_payload_check;
ALTER TABLE events ADD CONSTRAINT events_message_payload_check CHECK (
    (event_type = 'message' AND message IS NOT NULL AND jsonb_typeof(message) = 'object')
    OR (event_type = 'comment' AND (message IS NULL OR jsonb_typeof(message) = 'object'))
    OR (event_type NOT IN ('message', 'comment') AND message IS NULL)
);

-- +goose Down
ALTER TABLE events DROP CONSTRAINT events_message_payload_check;
UPDATE events SET message = NULL WHERE event_type = 'comment' AND message IS NOT NULL;
ALTER TABLE events ADD CONSTRAINT events_message_payload_check CHECK (
    (event_type = 'message' AND message IS NOT NULL AND jsonb_typeof(message) = 'object')
    OR (event_type <> 'message' AND message IS NULL)
);
DROP TABLE IF EXISTS workspace_attachments;
