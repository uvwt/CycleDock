-- +goose Up
ALTER TABLE workspace_model_routes
    ADD COLUMN api_key_ciphertext bytea;

-- +goose Down
ALTER TABLE workspace_model_routes
    DROP COLUMN api_key_ciphertext;

