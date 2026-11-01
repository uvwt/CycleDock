-- +goose Up
ALTER TABLE events ADD COLUMN message jsonb;
ALTER TABLE events DROP CONSTRAINT events_event_type_check;
ALTER TABLE events ADD CONSTRAINT events_event_type_check CHECK (event_type IN
  ('comment','progress','finding','result','checkin','confirm','reject','status_change','message'));
ALTER TABLE events ADD CONSTRAINT events_message_payload_check CHECK (
  (event_type = 'message' AND message IS NOT NULL AND jsonb_typeof(message) = 'object')
  OR (event_type <> 'message' AND message IS NULL));

-- +goose Down
ALTER TABLE events DROP CONSTRAINT events_message_payload_check;
-- 旧结构没有附件/选择字段；完整消息 JSON 转为 Progress 文本，回滚不丢内容或事件关联。
UPDATE events SET event_type='progress', content=message::text, message=NULL
WHERE event_type='message';
ALTER TABLE events DROP CONSTRAINT events_event_type_check;
ALTER TABLE events ADD CONSTRAINT events_event_type_check CHECK (event_type IN
  ('comment','progress','finding','result','checkin','confirm','reject','status_change'));
ALTER TABLE events DROP COLUMN message;
