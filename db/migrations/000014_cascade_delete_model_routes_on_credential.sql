-- +goose Up
-- 移除账号时级联删除关联的 workspace_model_routes
ALTER TABLE workspace_model_routes
    DROP CONSTRAINT workspace_model_routes_credential_fk,
    ADD CONSTRAINT workspace_model_routes_credential_fk
        FOREIGN KEY (workspace_id, credential_id)
        REFERENCES model_oauth_credentials(workspace_id, id)
        ON DELETE CASCADE;

-- +goose Down
ALTER TABLE workspace_model_routes
    DROP CONSTRAINT workspace_model_routes_credential_fk,
    ADD CONSTRAINT workspace_model_routes_credential_fk
        FOREIGN KEY (workspace_id, credential_id)
        REFERENCES model_oauth_credentials(workspace_id, id)
        ON DELETE RESTRICT;
