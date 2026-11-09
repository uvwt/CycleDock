-- +goose Up
ALTER TABLE card_turns DROP CONSTRAINT card_turns_source_shape_ck;
ALTER TABLE card_turns ADD CONSTRAINT card_turns_source_shape_ck CHECK (
  ((source = 'human'::text) AND (created_by_source_id IS NOT NULL) AND (schedule_id IS NULL) AND (scheduled_for IS NULL))
  OR ((source = 'schedule'::text) AND (created_by_source_id IS NULL) AND (schedule_id IS NOT NULL) AND (scheduled_for IS NOT NULL) AND (dispatch_mode = 'enqueue'::text))
  OR ((source = 'system'::text) AND (created_by_source_id IS NOT NULL) AND (schedule_id IS NULL) AND (scheduled_for IS NULL) AND (dispatch_mode = ANY (ARRAY['enqueue'::text, 'interrupt'::text])))
);

-- +goose Down
UPDATE card_turns SET dispatch_mode = 'enqueue' WHERE source = 'system' AND dispatch_mode = 'interrupt';
ALTER TABLE card_turns DROP CONSTRAINT card_turns_source_shape_ck;
ALTER TABLE card_turns ADD CONSTRAINT card_turns_source_shape_ck CHECK (
  ((source = 'human'::text) AND (created_by_source_id IS NOT NULL) AND (schedule_id IS NULL) AND (scheduled_for IS NULL))
  OR ((source = 'schedule'::text) AND (created_by_source_id IS NULL) AND (schedule_id IS NOT NULL) AND (scheduled_for IS NOT NULL) AND (dispatch_mode = 'enqueue'::text))
  OR ((source = 'system'::text) AND (created_by_source_id IS NOT NULL) AND (schedule_id IS NULL) AND (scheduled_for IS NULL) AND (dispatch_mode = 'enqueue'::text))
);
