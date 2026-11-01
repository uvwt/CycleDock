-- +goose Up
-- 用户回复只保存在 Message 中，Attempt 不再维护第二份结果正文。
ALTER TABLE turn_attempts DROP COLUMN result_summary;

-- +goose Down
ALTER TABLE turn_attempts ADD COLUMN result_summary text NOT NULL DEFAULT '';
