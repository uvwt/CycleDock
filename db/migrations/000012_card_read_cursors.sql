-- +goose Up
CREATE TABLE card_read_cursors (
    workspace_id uuid NOT NULL,
    source_id uuid NOT NULL,
    card_id uuid NOT NULL,
    last_read_message_sequence bigint DEFAULT 0 NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT card_read_cursors_sequence_check CHECK (last_read_message_sequence >= 0),
    CONSTRAINT card_read_cursors_pkey PRIMARY KEY (workspace_id, source_id, card_id),
    CONSTRAINT card_read_cursors_source_fkey FOREIGN KEY (workspace_id, source_id)
        REFERENCES sources (workspace_id, id) ON DELETE CASCADE,
    CONSTRAINT card_read_cursors_card_fkey FOREIGN KEY (workspace_id, card_id)
        REFERENCES cards (workspace_id, id) ON DELETE CASCADE
);
CREATE INDEX card_read_cursors_card_idx
    ON card_read_cursors (workspace_id, card_id);

-- 上线时把已有可见消息作为已读基线，避免首次启用服务端游标后历史讨论全部亮蓝点。
INSERT INTO card_read_cursors (
    workspace_id, source_id, card_id, last_read_message_sequence
)
SELECT
    c.workspace_id,
    s.id,
    c.id,
    COALESCE(MAX(e.sequence), 0)
FROM cards c
JOIN sources s
  ON s.workspace_id = c.workspace_id
 AND s.kind = 'human'
LEFT JOIN events e
  ON e.workspace_id = c.workspace_id
 AND e.card_id = c.id
 AND e.event_type IN ('comment', 'message')
GROUP BY c.workspace_id, s.id, c.id;

-- +goose Down
DROP TABLE IF EXISTS card_read_cursors;
