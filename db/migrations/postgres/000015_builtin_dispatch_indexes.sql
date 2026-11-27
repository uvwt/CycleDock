-- +goose Up
-- 租户公平排序只读最近一次开始时间，额度统计只读运行中的 Attempt。
CREATE INDEX turn_attempts_runner_started_idx
    ON turn_attempts (workspace_id, runner_source_id, started_at DESC)
    WHERE started_at IS NOT NULL;
CREATE INDEX turn_attempts_runner_running_idx
    ON turn_attempts (workspace_id, runner_source_id)
    WHERE status='running';

-- +goose Down
DROP INDEX turn_attempts_runner_running_idx;
DROP INDEX turn_attempts_runner_started_idx;
