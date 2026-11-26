-- +goose Up
-- 每个项目有一张主聊天 Discussion。
ALTER TABLE projects ADD COLUMN main_discussion_id uuid;
ALTER TABLE projects
  ADD CONSTRAINT projects_main_discussion_fk
  FOREIGN KEY (main_discussion_id) REFERENCES cards (id);
CREATE UNIQUE INDEX projects_main_discussion_uq
  ON projects (main_discussion_id)
  WHERE main_discussion_id IS NOT NULL;

-- +goose Down
ALTER TABLE projects DROP CONSTRAINT IF EXISTS projects_main_discussion_fk;
DROP INDEX IF EXISTS projects_main_discussion_uq;
ALTER TABLE projects DROP COLUMN IF EXISTS main_discussion_id;
