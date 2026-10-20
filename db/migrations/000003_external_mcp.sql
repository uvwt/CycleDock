-- +goose Up

ALTER TABLE cards
    ADD COLUMN create_idempotency_key text NULL,
    ADD COLUMN create_request_hash text NULL,
    ADD CONSTRAINT cards_create_request_identity_ck CHECK (
        (create_idempotency_key IS NULL AND create_request_hash IS NULL)
        OR
        (create_idempotency_key IS NOT NULL AND btrim(create_idempotency_key) <> '' AND create_request_hash IS NOT NULL)
    );

CREATE UNIQUE INDEX cards_source_create_idempotency_uq
    ON cards(workspace_id, created_by_source_id, create_idempotency_key)
    WHERE create_idempotency_key IS NOT NULL;

CREATE UNIQUE INDEX events_source_type_idempotency_uq
    ON events(workspace_id, actor_source_id, event_type, idempotency_key)
    WHERE idempotency_key IS NOT NULL
      AND event_type IN ('progress', 'finding', 'checkin', 'confirm');

CREATE UNIQUE INDEX events_confirm_source_uq
    ON events(workspace_id, card_id, actor_source_id)
    WHERE event_type = 'confirm';

CREATE UNIQUE INDEX triggers_source_followup_idempotency_uq
    ON triggers(workspace_id, created_by_source_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL
      AND cause IN ('manual_retry', 'system_followup');

ALTER TABLE agent_runs
    ADD COLUMN finish_idempotency_key text NULL,
    ADD COLUMN finish_request_hash text NULL,
    ADD CONSTRAINT agent_runs_finish_request_identity_ck CHECK (
        (finish_idempotency_key IS NULL AND finish_request_hash IS NULL)
        OR
        (finish_idempotency_key IS NOT NULL AND btrim(finish_idempotency_key) <> '' AND finish_request_hash IS NOT NULL)
    );

CREATE TABLE oauth_clients (
    client_id text PRIMARY KEY CHECK (btrim(client_id) <> ''),
    client_name text NOT NULL CHECK (btrim(client_name) <> ''),
    redirect_uris text[] NOT NULL CHECK (cardinality(redirect_uris) BETWEEN 1 AND 20),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE oauth_grants (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    human_account_id uuid NOT NULL,
    human_source_id uuid NOT NULL,
    client_id text NOT NULL REFERENCES oauth_clients(client_id) ON DELETE CASCADE,
    source_id uuid NOT NULL,
    resource text NOT NULL CHECK (btrim(resource) <> ''),
    scope text NOT NULL CHECK (btrim(scope) <> ''),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    revoked_at timestamptz NULL,
    UNIQUE (workspace_id, client_id, resource),
    UNIQUE (workspace_id, source_id),
    CONSTRAINT oauth_grants_human_membership_fk
        FOREIGN KEY (workspace_id, human_account_id, human_source_id)
        REFERENCES workspace_memberships(workspace_id, human_account_id, source_id)
        ON DELETE CASCADE,
    CONSTRAINT oauth_grants_source_fk
        FOREIGN KEY (workspace_id, source_id)
        REFERENCES sources(workspace_id, id)
        ON DELETE CASCADE,
    CONSTRAINT oauth_grants_distinct_sources_ck CHECK (human_source_id <> source_id)
);

CREATE INDEX oauth_grants_workspace_client_idx
    ON oauth_grants(workspace_id, client_id);

CREATE TABLE oauth_authorization_codes (
    code_digest bytea PRIMARY KEY CHECK (octet_length(code_digest) = 32),
    grant_id uuid NOT NULL REFERENCES oauth_grants(id) ON DELETE CASCADE,
    redirect_uri text NOT NULL CHECK (btrim(redirect_uri) <> ''),
    code_challenge text NOT NULL CHECK (length(code_challenge) BETWEEN 43 AND 128),
    scope text NOT NULL CHECK (btrim(scope) <> ''),
    resource text NOT NULL CHECK (btrim(resource) <> ''),
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (expires_at > created_at)
);

CREATE INDEX oauth_authorization_codes_expiry_idx
    ON oauth_authorization_codes(expires_at);

CREATE TABLE oauth_tokens (
    id uuid PRIMARY KEY,
    grant_id uuid NOT NULL REFERENCES oauth_grants(id) ON DELETE CASCADE,
    family_id uuid NOT NULL,
    access_token_digest bytea NOT NULL UNIQUE CHECK (octet_length(access_token_digest) = 32),
    refresh_token_digest bytea NOT NULL UNIQUE CHECK (octet_length(refresh_token_digest) = 32),
    access_expires_at timestamptz NOT NULL,
    refresh_expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    revoked_at timestamptz NULL,
    CHECK (access_expires_at > created_at),
    CHECK (refresh_expires_at > access_expires_at)
);

CREATE INDEX oauth_tokens_family_idx
    ON oauth_tokens(family_id);

CREATE INDEX oauth_tokens_access_expiry_idx
    ON oauth_tokens(access_expires_at);

CREATE INDEX oauth_tokens_refresh_expiry_idx
    ON oauth_tokens(refresh_expires_at);

-- +goose Down

DROP TABLE IF EXISTS oauth_tokens;
DROP TABLE IF EXISTS oauth_authorization_codes;
DROP TABLE IF EXISTS oauth_grants;
DROP TABLE IF EXISTS oauth_clients;

ALTER TABLE agent_runs
    DROP CONSTRAINT IF EXISTS agent_runs_finish_request_identity_ck,
    DROP COLUMN IF EXISTS finish_request_hash,
    DROP COLUMN IF EXISTS finish_idempotency_key;

DROP INDEX IF EXISTS triggers_source_followup_idempotency_uq;
DROP INDEX IF EXISTS events_confirm_source_uq;
DROP INDEX IF EXISTS events_source_type_idempotency_uq;
DROP INDEX IF EXISTS cards_source_create_idempotency_uq;

ALTER TABLE cards
    DROP CONSTRAINT IF EXISTS cards_create_request_identity_ck,
    DROP COLUMN IF EXISTS create_request_hash,
    DROP COLUMN IF EXISTS create_idempotency_key;
