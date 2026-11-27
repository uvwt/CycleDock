-- +goose Up
-- 项目背景与 Agent 工作要求统一为项目说明，删除独立字段前保留已有内容。
UPDATE projects
SET description = CASE
    WHEN btrim(description) = '' THEN instructions
    WHEN btrim(description) = btrim(instructions) THEN description
    ELSE description || E'\n\n' || instructions
END
WHERE btrim(instructions) <> '';

ALTER TABLE projects DROP COLUMN instructions;

-- +goose Down
-- 合并后的内容继续保留在项目说明中，回滚只恢复字段结构。
ALTER TABLE projects ADD COLUMN instructions text NOT NULL DEFAULT '';
