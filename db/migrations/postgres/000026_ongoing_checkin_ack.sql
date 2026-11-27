-- +goose Up
ALTER TABLE events DROP CONSTRAINT events_event_type_check;
ALTER TABLE events ADD CONSTRAINT events_event_type_check CHECK (event_type IN
  ('comment','progress','finding','result','checkin','confirm','reject','status_change','message','checkin_ack'));

-- +goose Down
DELETE FROM events WHERE event_type = 'checkin_ack';
ALTER TABLE events DROP CONSTRAINT events_event_type_check;
ALTER TABLE events ADD CONSTRAINT events_event_type_check CHECK (event_type IN
  ('comment','progress','finding','result','checkin','confirm','reject','status_change','message'));
