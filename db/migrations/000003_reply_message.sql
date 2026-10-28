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
ALTER TABLE events DROP CONSTRAINT events_event_type_check;
ALTER TABLE events ADD CONSTRAINT events_event_type_check CHECK (event_type IN
  ('comment','progress','finding','result','checkin','confirm','reject','status_change'));
ALTER TABLE events DROP COLUMN message;
