-- +goose Up
-- 设备账号与 OIDC 登录账号独立；既有连接的账号标识未知，重新授权后补充。
ALTER TABLE workspace_nexusdock_connections ADD COLUMN authorized_subject text NOT NULL DEFAULT '';
ALTER TABLE nexusdock_auth_flows DROP COLUMN automatic, DROP COLUMN return_to, DROP COLUMN expected_subject;
DROP TABLE nexusdock_auth_preferences;

-- +goose Down
CREATE TABLE nexusdock_auth_preferences (
 workspace_id uuid PRIMARY KEY REFERENCES workspaces(id) ON DELETE CASCADE,
 auto_login_declined boolean NOT NULL DEFAULT false
);
ALTER TABLE nexusdock_auth_flows
 ADD COLUMN automatic boolean NOT NULL DEFAULT false,
 ADD COLUMN return_to text NOT NULL DEFAULT '/',
 ADD COLUMN expected_subject text NOT NULL DEFAULT '';
ALTER TABLE workspace_nexusdock_connections DROP COLUMN authorized_subject;
