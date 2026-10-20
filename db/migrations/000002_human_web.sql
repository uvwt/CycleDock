-- +goose Up

CREATE TABLE human_accounts (
    id uuid PRIMARY KEY,
    display_name text NOT NULL CHECK (btrim(display_name) <> ''),
    email_snapshot text NULL,
    is_local_admin boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX human_accounts_single_local_admin_uq
    ON human_accounts (is_local_admin)
    WHERE is_local_admin;

CREATE TABLE oidc_identities (
    issuer text NOT NULL CHECK (btrim(issuer) <> ''),
    subject text NOT NULL CHECK (btrim(subject) <> ''),
    human_account_id uuid NOT NULL REFERENCES human_accounts(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (issuer, subject)
);

CREATE INDEX oidc_identities_human_account_idx
    ON oidc_identities (human_account_id);

CREATE TABLE workspace_memberships (
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    human_account_id uuid NOT NULL REFERENCES human_accounts(id) ON DELETE CASCADE,
    source_id uuid NOT NULL,
    role text NOT NULL CHECK (role IN ('owner', 'member')),
    is_personal boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, human_account_id),
    UNIQUE (source_id),
    UNIQUE (workspace_id, human_account_id, source_id),
    CONSTRAINT workspace_memberships_source_fk
        FOREIGN KEY (workspace_id, source_id)
        REFERENCES sources(workspace_id, id)
        ON DELETE CASCADE
);

CREATE UNIQUE INDEX workspace_memberships_personal_account_uq
    ON workspace_memberships (human_account_id)
    WHERE is_personal;

CREATE UNIQUE INDEX workspace_memberships_personal_workspace_uq
    ON workspace_memberships (workspace_id)
    WHERE is_personal;

CREATE TABLE web_sessions (
    id uuid PRIMARY KEY,
    human_account_id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    source_id uuid NOT NULL,
    token_digest bytea NOT NULL UNIQUE CHECK (octet_length(token_digest) = 32),
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT web_sessions_membership_fk
        FOREIGN KEY (workspace_id, human_account_id, source_id)
        REFERENCES workspace_memberships(workspace_id, human_account_id, source_id)
        ON DELETE CASCADE
);

CREATE INDEX web_sessions_expiry_idx ON web_sessions(expires_at);

CREATE TABLE web_auth_flows (
    state_digest bytea PRIMARY KEY CHECK (octet_length(state_digest) = 32),
    provider text NOT NULL CHECK (provider = 'oidc'),
    code_verifier text NOT NULL CHECK (btrim(code_verifier) <> ''),
    nonce text NOT NULL CHECK (btrim(nonce) <> ''),
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX web_auth_flows_expiry_idx ON web_auth_flows(expires_at);

-- +goose Down

DROP TABLE IF EXISTS web_auth_flows;
DROP TABLE IF EXISTS web_sessions;
DROP TABLE IF EXISTS workspace_memberships;
DROP TABLE IF EXISTS oidc_identities;
DROP TABLE IF EXISTS human_accounts;
