-- +goose Up
-- 扩展 workspace_attachments 以支持纯文本/日志/代码附件（gzip 透明压缩落盘），
-- 明确区分逻辑原始字节数 size_bytes 与物理磁盘占用 stored_size_bytes，并允许文本类附件宽高为 0。
ALTER TABLE workspace_attachments
    ADD COLUMN kind text NOT NULL DEFAULT 'image' CHECK (kind IN ('image', 'text')),
    ADD COLUMN file_name text NOT NULL DEFAULT '',
    ADD COLUMN storage_encoding text NOT NULL DEFAULT 'identity' CHECK (storage_encoding IN ('identity', 'gzip')),
    ADD COLUMN stored_size_bytes bigint NOT NULL DEFAULT 0 CHECK (stored_size_bytes >= 0),
    ADD COLUMN line_count integer NOT NULL DEFAULT 0 CHECK (line_count >= 0);

UPDATE workspace_attachments
SET stored_size_bytes = size_bytes
WHERE stored_size_bytes = 0;

ALTER TABLE workspace_attachments
    DROP CONSTRAINT IF EXISTS workspace_attachments_stored_size_bytes_check,
    ADD CONSTRAINT workspace_attachments_stored_size_bytes_check CHECK (stored_size_bytes > 0);

ALTER TABLE workspace_attachments
    DROP CONSTRAINT IF EXISTS workspace_attachments_width_check,
    DROP CONSTRAINT IF EXISTS workspace_attachments_height_check,
    ADD CONSTRAINT workspace_attachments_dimensions_check CHECK (
        (kind = 'image' AND width > 0 AND height > 0)
        OR (kind = 'text' AND width = 0 AND height = 0)
    );

DROP INDEX IF EXISTS workspace_attachments_ws_sha256_idx;
CREATE INDEX workspace_attachments_ws_sha256_idx
    ON workspace_attachments (workspace_id, kind, sha256)
    WHERE purged_at IS NULL;

-- +goose Down
DROP INDEX IF EXISTS workspace_attachments_ws_sha256_idx;
CREATE INDEX workspace_attachments_ws_sha256_idx
    ON workspace_attachments (workspace_id, sha256)
    WHERE purged_at IS NULL;

DELETE FROM workspace_attachments WHERE kind <> 'image';

ALTER TABLE workspace_attachments
    DROP CONSTRAINT IF EXISTS workspace_attachments_dimensions_check,
    ADD CONSTRAINT workspace_attachments_width_check CHECK (width > 0),
    ADD CONSTRAINT workspace_attachments_height_check CHECK (height > 0);

ALTER TABLE workspace_attachments
    DROP COLUMN IF EXISTS line_count,
    DROP COLUMN IF EXISTS stored_size_bytes,
    DROP COLUMN IF EXISTS storage_encoding,
    DROP COLUMN IF EXISTS file_name,
    DROP COLUMN IF EXISTS kind;
