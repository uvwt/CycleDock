-- +goose Up
-- 修复所有联合外键未显式指定 ON DELETE SET NULL (<nullable_col>) 列名的问题。
-- 在 PostgreSQL 中，联合外键若仅写 ON DELETE SET NULL 而不指定列名，删除父记录时会尝试
-- 将外键中的全部列（含 NOT NULL 的 workspace_id / card_id）一并置空，导致触发非空约束失败
-- （例如删除带有图片/文本附件的卡片时 workspace_attachments.workspace_id 报 23502）。

ALTER TABLE workspace_attachments
    DROP CONSTRAINT IF EXISTS workspace_attachments_card_fk,
    ADD CONSTRAINT workspace_attachments_card_fk
        FOREIGN KEY (workspace_id, card_id)
        REFERENCES cards(workspace_id, id)
        ON DELETE SET NULL (card_id);

ALTER TABLE events
    DROP CONSTRAINT IF EXISTS events_source_card_fk,
    ADD CONSTRAINT events_source_card_fk
        FOREIGN KEY (workspace_id, source_card_id)
        REFERENCES cards(workspace_id, id)
        ON DELETE SET NULL (source_card_id);

ALTER TABLE remote_channels
    DROP CONSTRAINT IF EXISTS remote_channels_card_fk,
    ADD CONSTRAINT remote_channels_card_fk
        FOREIGN KEY (workspace_id, target_card_id)
        REFERENCES cards(workspace_id, id)
        ON DELETE SET NULL (target_card_id);

ALTER TABLE remote_channels
    DROP CONSTRAINT IF EXISTS remote_channels_creator_fk,
    ADD CONSTRAINT remote_channels_creator_fk
        FOREIGN KEY (workspace_id, created_by_source_id)
        REFERENCES sources(workspace_id, id)
        ON DELETE SET NULL (created_by_source_id);

ALTER TABLE card_turns
    DROP CONSTRAINT IF EXISTS card_turns_source_event_fk,
    ADD CONSTRAINT card_turns_source_event_fk
        FOREIGN KEY (workspace_id, card_id, source_event_id)
        REFERENCES events(workspace_id, card_id, id)
        ON DELETE SET NULL (source_event_id);

ALTER TABLE card_tool_approvals
    DROP CONSTRAINT IF EXISTS card_tool_approvals_source_fk,
    ADD CONSTRAINT card_tool_approvals_source_fk
        FOREIGN KEY (workspace_id, resolved_by_source_id)
        REFERENCES sources(workspace_id, id)
        ON DELETE SET NULL (resolved_by_source_id);

ALTER TABLE card_tool_whitelists
    DROP CONSTRAINT IF EXISTS card_tool_whitelists_source_fk,
    ADD CONSTRAINT card_tool_whitelists_source_fk
        FOREIGN KEY (workspace_id, created_by_source_id)
        REFERENCES sources(workspace_id, id)
        ON DELETE SET NULL (created_by_source_id);

-- +goose Down
ALTER TABLE card_tool_whitelists
    DROP CONSTRAINT IF EXISTS card_tool_whitelists_source_fk,
    ADD CONSTRAINT card_tool_whitelists_source_fk
        FOREIGN KEY (workspace_id, created_by_source_id)
        REFERENCES sources(workspace_id, id)
        ON DELETE SET NULL;

ALTER TABLE card_tool_approvals
    DROP CONSTRAINT IF EXISTS card_tool_approvals_source_fk,
    ADD CONSTRAINT card_tool_approvals_source_fk
        FOREIGN KEY (workspace_id, resolved_by_source_id)
        REFERENCES sources(workspace_id, id)
        ON DELETE SET NULL;

ALTER TABLE card_turns
    DROP CONSTRAINT IF EXISTS card_turns_source_event_fk,
    ADD CONSTRAINT card_turns_source_event_fk
        FOREIGN KEY (workspace_id, card_id, source_event_id)
        REFERENCES events(workspace_id, card_id, id)
        ON DELETE SET NULL;

ALTER TABLE remote_channels
    DROP CONSTRAINT IF EXISTS remote_channels_creator_fk,
    ADD CONSTRAINT remote_channels_creator_fk
        FOREIGN KEY (workspace_id, created_by_source_id)
        REFERENCES sources(workspace_id, id)
        ON DELETE SET NULL;

ALTER TABLE remote_channels
    DROP CONSTRAINT IF EXISTS remote_channels_card_fk,
    ADD CONSTRAINT remote_channels_card_fk
        FOREIGN KEY (workspace_id, target_card_id)
        REFERENCES cards(workspace_id, id)
        ON DELETE SET NULL;

ALTER TABLE events
    DROP CONSTRAINT IF EXISTS events_source_card_fk,
    ADD CONSTRAINT events_source_card_fk
        FOREIGN KEY (workspace_id, source_card_id)
        REFERENCES cards(workspace_id, id)
        ON DELETE SET NULL;

ALTER TABLE workspace_attachments
    DROP CONSTRAINT IF EXISTS workspace_attachments_card_fk,
    ADD CONSTRAINT workspace_attachments_card_fk
        FOREIGN KEY (workspace_id, card_id)
        REFERENCES cards(workspace_id, id)
        ON DELETE SET NULL;
