-- +goose Up
-- 每个项目有一张主聊天 Discussion。别的卡片上的可见消息写成 notice，
-- 不创建 Turn，也不进入那张卡自己的 Session。
ALTER TABLE projects ADD COLUMN main_discussion_id uuid;
ALTER TABLE projects
  ADD CONSTRAINT projects_main_discussion_fk
  FOREIGN KEY (main_discussion_id) REFERENCES cards (id);
CREATE UNIQUE INDEX projects_main_discussion_uq
  ON projects (main_discussion_id)
  WHERE main_discussion_id IS NOT NULL;

ALTER TABLE events DROP CONSTRAINT events_event_type_check;
ALTER TABLE events ADD CONSTRAINT events_event_type_check CHECK (event_type IN
  ('comment','progress','finding','result','checkin','confirm','reject','status_change','message','notice'));

CREATE UNIQUE INDEX events_notice_source_uq
  ON events (workspace_id, idempotency_key)
  WHERE event_type = 'notice' AND idempotency_key IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS events_notice_source_uq;
DELETE FROM events WHERE event_type = 'notice';
ALTER TABLE events DROP CONSTRAINT events_event_type_check;
ALTER TABLE events ADD CONSTRAINT events_event_type_check CHECK (event_type IN
  ('comment','progress','finding','result','checkin','confirm','reject','status_change','message'));
ALTER TABLE projects DROP CONSTRAINT IF EXISTS projects_main_discussion_fk;
DROP INDEX IF EXISTS projects_main_discussion_uq;
ALTER TABLE projects DROP COLUMN IF EXISTS main_discussion_id;
