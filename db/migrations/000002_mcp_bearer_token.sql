-- +goose Up
-- 旧字段只保存环境变量名，不包含真实密钥；升级后需用户在设置中重新填写 Token。
ALTER TABLE workspace_mcp_connections
    ADD COLUMN bearer_token_ciphertext bytea;
ALTER TABLE workspace_mcp_connections
    DROP COLUMN bearer_token_env;

-- +goose Down
ALTER TABLE workspace_mcp_connections
    ADD COLUMN bearer_token_env text NOT NULL DEFAULT '';
ALTER TABLE workspace_mcp_connections
    DROP COLUMN bearer_token_ciphertext;
