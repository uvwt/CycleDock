-- +goose Up
ALTER TABLE card_schedules DROP CONSTRAINT card_schedules_frequency_check;
ALTER TABLE card_schedules ADD CONSTRAINT card_schedules_frequency_check
    CHECK (frequency IN ('daily', 'weekly', 'monthly', 'interval'));

ALTER TABLE card_schedules ADD COLUMN interval_minutes integer;

ALTER TABLE card_schedules ALTER COLUMN time_of_day DROP NOT NULL;

ALTER TABLE card_schedules DROP CONSTRAINT card_schedules_shape_ck;
ALTER TABLE card_schedules ADD CONSTRAINT card_schedules_shape_ck CHECK (
    (frequency = 'daily' AND time_of_day IS NOT NULL AND day_of_week IS NULL AND day_of_month IS NULL AND interval_minutes IS NULL)
    OR (frequency = 'weekly' AND time_of_day IS NOT NULL AND day_of_week IS NOT NULL AND day_of_month IS NULL AND interval_minutes IS NULL)
    OR (frequency = 'monthly' AND time_of_day IS NOT NULL AND day_of_week IS NULL AND day_of_month IS NOT NULL AND interval_minutes IS NULL)
    OR (frequency = 'interval' AND time_of_day IS NULL AND day_of_week IS NULL AND day_of_month IS NULL AND interval_minutes IS NOT NULL AND interval_minutes >= 1 AND interval_minutes <= 1440)
);

-- +goose Down
DELETE FROM card_schedules WHERE frequency = 'interval';

ALTER TABLE card_schedules DROP CONSTRAINT card_schedules_shape_ck;
ALTER TABLE card_schedules ALTER COLUMN time_of_day SET NOT NULL;
ALTER TABLE card_schedules DROP COLUMN interval_minutes;

ALTER TABLE card_schedules DROP CONSTRAINT card_schedules_frequency_check;
ALTER TABLE card_schedules ADD CONSTRAINT card_schedules_frequency_check
    CHECK (frequency IN ('daily', 'weekly', 'monthly'));

ALTER TABLE card_schedules ADD CONSTRAINT card_schedules_shape_ck CHECK (
    (frequency = 'daily' AND day_of_week IS NULL AND day_of_month IS NULL)
    OR (frequency = 'weekly' AND day_of_week IS NOT NULL AND day_of_month IS NULL)
    OR (frequency = 'monthly' AND day_of_week IS NULL AND day_of_month IS NOT NULL)
);
