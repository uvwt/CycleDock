-- +goose Up
-- NexusDock 一体登录不提升权限：仅记录经用户同意的 OAuth 状态及拒绝偏好。
ALTER TABLE nexusdock_auth_flows
  ADD COLUMN automatic boolean NOT NULL DEFAULT false,
  ADD COLUMN return_to text NOT NULL DEFAULT '/',
  ADD COLUMN expected_subject text NOT NULL DEFAULT '';
CREATE TABLE nexusdock_auth_preferences (
    workspace_id uuid PRIMARY KEY REFERENCES workspaces(id) ON DELETE CASCADE,
    auto_login_declined boolean NOT NULL DEFAULT false
);

-- +goose Down
DROP TABLE nexusdock_auth_preferences;
ALTER TABLE nexusdock_auth_flows
  DROP COLUMN automatic,
  DROP COLUMN return_to,
  DROP COLUMN expected_subject;
