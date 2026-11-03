-- +goose Up
-- 分支合并前，来源卡片曾使用版本 18；旧库已记录该版本，会跳过现有的模型路由迁移。
-- 在版本 21 收敛两条迁移路径，保留已有来源数据，并补齐被跳过的模型路由字段。
ALTER TABLE card_turns
    ADD COLUMN IF NOT EXISTS preferred_model_route_id uuid REFERENCES workspace_model_routes(id) ON DELETE SET NULL;

ALTER TABLE events ADD COLUMN IF NOT EXISTS source_card_id uuid;
-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'events'::regclass AND conname = 'events_source_card_fk'
    ) THEN
        ALTER TABLE events ADD CONSTRAINT events_source_card_fk
            FOREIGN KEY (workspace_id, source_card_id) REFERENCES cards(workspace_id, id) ON DELETE SET NULL;
    END IF;
END;
$$;
-- +goose StatementEnd

-- +goose Down
ALTER TABLE events DROP CONSTRAINT events_source_card_fk;
ALTER TABLE events DROP COLUMN source_card_id;

