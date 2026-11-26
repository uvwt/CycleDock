-- +goose Up
-- 加速后台保留策略清理任务定位存在 compaction 摘要的会话与最新摘要记录
CREATE INDEX agent_session_events_compaction_idx
    ON agent_session_events (app_name, user_id, session_id, occurred_at DESC, sequence DESC)
    WHERE (event -> 'actions' -> 'compaction') IS NOT NULL;

-- 加速按会话与创建时间分批清理过期非图片工具产物
CREATE INDEX agent_artifacts_retention_idx
    ON agent_artifacts (app_name, user_id, session_id, created_at)
    WHERE mime_type NOT LIKE 'image/%';

-- +goose Down
DROP INDEX IF EXISTS agent_artifacts_retention_idx;
DROP INDEX IF EXISTS agent_session_events_compaction_idx;
