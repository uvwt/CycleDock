-- +goose Up
-- 为项目增加「远程通道唯一主推路由」策略：
--   - primary_push_mode:
--       'last_active'（默认）：自动跟随当前项目下已启用且有入站绑定记录的双向对话通道中最近活跃（last_inbound_at 最新）的那 1 个；无活跃通道则不推远程。
--       'channel'：仅推送给 primary_push_channel_id 指定的唯一双向对话通道；目标被删除/禁用/切为定时推送时安全静默，绝不退化为广播。
--       'none'：仅网页端 / 免打扰，不向任何远程通道推主聊天或系统通知。
--   - primary_push_channel_id: 仅在 primary_push_mode = 'channel' 时允许非空；目标通道被删除时自动置空。
ALTER TABLE projects
    ADD COLUMN IF NOT EXISTS primary_push_mode text NOT NULL DEFAULT 'last_active'::text,
    ADD COLUMN IF NOT EXISTS primary_push_channel_id uuid;

ALTER TABLE projects
    DROP CONSTRAINT IF EXISTS projects_primary_push_mode_ck;

ALTER TABLE projects
    ADD CONSTRAINT projects_primary_push_mode_ck
    CHECK (primary_push_mode = ANY (ARRAY['last_active'::text, 'channel'::text, 'none'::text]));

ALTER TABLE projects
    DROP CONSTRAINT IF EXISTS projects_primary_push_channel_shape_ck;

ALTER TABLE projects
    ADD CONSTRAINT projects_primary_push_channel_shape_ck
    CHECK (primary_push_mode = 'channel'::text OR primary_push_channel_id IS NULL);

ALTER TABLE projects
    DROP CONSTRAINT IF EXISTS projects_primary_push_channel_fk;

ALTER TABLE projects
    ADD CONSTRAINT projects_primary_push_channel_fk
    FOREIGN KEY (primary_push_channel_id) REFERENCES remote_channels(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS projects_primary_push_channel_idx
    ON projects (workspace_id, primary_push_channel_id)
    WHERE primary_push_channel_id IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS projects_primary_push_channel_idx;
ALTER TABLE projects DROP CONSTRAINT IF EXISTS projects_primary_push_channel_fk;
ALTER TABLE projects DROP CONSTRAINT IF EXISTS projects_primary_push_channel_shape_ck;
ALTER TABLE projects DROP CONSTRAINT IF EXISTS projects_primary_push_mode_ck;
ALTER TABLE projects
    DROP COLUMN IF EXISTS primary_push_channel_id,
    DROP COLUMN IF EXISTS primary_push_mode;
