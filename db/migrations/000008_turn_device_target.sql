-- +goose Up
-- 设备选择是每次输入的偏好快照，不能从会随用户切换而变化的全局设置推导。
ALTER TABLE card_turns
 ADD COLUMN device_node_id text NOT NULL DEFAULT '',
 ADD COLUMN device_name text NOT NULL DEFAULT '',
 ADD CONSTRAINT card_turns_device_target_ck CHECK (
   char_length(device_node_id) <= 200 AND char_length(device_name) <= 200
   AND (device_node_id <> '' OR device_name = '')
 );

-- +goose Down
ALTER TABLE card_turns DROP CONSTRAINT card_turns_device_target_ck,
 DROP COLUMN device_node_id, DROP COLUMN device_name;
