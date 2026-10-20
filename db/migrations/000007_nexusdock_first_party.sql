-- +goose Up

CREATE TABLE nexusdock_oauth_clients (
    resource text PRIMARY KEY,
    client_id text NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE nexusdock_auth_flows (
    state_digest bytea PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    human_account_id uuid NOT NULL REFERENCES human_accounts(id) ON DELETE CASCADE,
    source_id uuid NOT NULL,
    client_id text NOT NULL,
    code_verifier text NOT NULL,
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT nexusdock_auth_flows_source_fk
        FOREIGN KEY (workspace_id, source_id)
        REFERENCES sources(workspace_id, id)
        ON DELETE CASCADE
);

CREATE INDEX nexusdock_auth_flows_expiry_idx
    ON nexusdock_auth_flows(expires_at);

CREATE TABLE workspace_nexusdock_connections (
    workspace_id uuid PRIMARY KEY REFERENCES workspaces(id) ON DELETE CASCADE,
    resource text NOT NULL,
    client_id text NOT NULL,
    access_token_ciphertext bytea NOT NULL,
    refresh_token_ciphertext bytea NOT NULL,
    access_expires_at timestamptz NOT NULL,
    connected_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down

DROP TABLE IF EXISTS workspace_nexusdock_connections;
DROP TABLE IF EXISTS nexusdock_auth_flows;
DROP TABLE IF EXISTS nexusdock_oauth_clients;
