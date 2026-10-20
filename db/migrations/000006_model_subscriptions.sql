-- +goose Up

ALTER TABLE workspace_agent_settings
    ADD COLUMN provider_type text NOT NULL DEFAULT 'openai_compatible'
    CHECK (provider_type IN ('openai_compatible', 'chatgpt', 'grok'));

ALTER TABLE workspace_agent_settings
    DROP CONSTRAINT workspace_agent_settings_enabled_ck;

ALTER TABLE workspace_agent_settings
    ADD CONSTRAINT workspace_agent_settings_enabled_ck CHECK (
        NOT enabled OR (
            btrim(model) <> ''
            AND (
                provider_type <> 'openai_compatible'
                OR btrim(provider_base_url) <> ''
            )
        )
    );

CREATE TABLE model_oauth_credentials (
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    provider text NOT NULL CHECK (provider IN ('chatgpt', 'grok')),
    credential_ciphertext bytea NOT NULL CHECK (octet_length(credential_ciphertext) > 28),
    expires_at timestamptz NOT NULL,
    display_label text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, provider)
);

CREATE TABLE model_auth_flows (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    provider text NOT NULL CHECK (provider IN ('chatgpt', 'grok')),
    state_digest bytea NULL CHECK (state_digest IS NULL OR octet_length(state_digest) = 32),
    payload_ciphertext bytea NOT NULL CHECK (octet_length(payload_ciphertext) > 28),
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX model_auth_flows_state_idx
    ON model_auth_flows(state_digest)
    WHERE state_digest IS NOT NULL;

CREATE INDEX model_auth_flows_workspace_idx
    ON model_auth_flows(workspace_id, provider, expires_at);

-- +goose Down

DROP TABLE IF EXISTS model_auth_flows;
DROP TABLE IF EXISTS model_oauth_credentials;

ALTER TABLE workspace_agent_settings
    DROP CONSTRAINT workspace_agent_settings_enabled_ck;

-- 旧 schema 不理解 subscription provider。降级时保留设置行，但先停止这些 Agent，
-- 避免用空 provider_base_url 伪装成 OpenAI-compatible 配置。
UPDATE workspace_agent_settings SET enabled = false WHERE provider_type <> 'openai_compatible';

ALTER TABLE workspace_agent_settings
    ADD CONSTRAINT workspace_agent_settings_enabled_ck CHECK (
        NOT enabled OR (btrim(provider_base_url) <> '' AND btrim(model) <> '')
    );

ALTER TABLE workspace_agent_settings
    DROP COLUMN IF EXISTS provider_type;
