-- +goose Up
-- 记录卡片本身（例如新建待办时）直接附带的图片附件有序关联。
-- 与聊天消息 events.message 中的图片引用区分开，避免把聊天过程图片混入卡片初始附件列表。
CREATE TABLE card_attachments (
    workspace_id uuid NOT NULL,
    card_id uuid NOT NULL,
    attachment_id uuid NOT NULL,
    position integer NOT NULL CHECK (position >= 0),
    created_at timestamp with time zone NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, card_id, attachment_id),
    CONSTRAINT card_attachments_card_fk
        FOREIGN KEY (workspace_id, card_id)
        REFERENCES cards(workspace_id, id)
        ON DELETE CASCADE,
    CONSTRAINT card_attachments_attachment_fk
        FOREIGN KEY (attachment_id)
        REFERENCES workspace_attachments(id)
        ON DELETE CASCADE
);

CREATE INDEX card_attachments_card_position_idx
    ON card_attachments (workspace_id, card_id, position ASC, created_at ASC);

-- +goose Down
DROP TABLE IF EXISTS card_attachments;
