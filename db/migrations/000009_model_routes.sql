-- +goose Up

ALTER TABLE model_oauth_credentials
    ADD COLUMN id uuid;

UPDATE model_oauth_credentials
SET id = md5(workspace_id::text || ':' || provider)::uuid;

ALTER TABLE model_oauth_credentials
    ALTER COLUMN id SET NOT NULL,
    DROP CONSTRAINT model_oauth_credentials_pkey,
    ADD PRIMARY KEY (id),
    ADD CONSTRAINT model_oauth_credentials_workspace_id_id_key UNIQUE (workspace_id, id),
    ADD COLUMN account_subject text NOT NULL DEFAULT '';

CREATE UNIQUE INDEX model_oauth_credentials_account_identity_idx
    ON model_oauth_credentials(workspace_id, provider, account_subject)
    WHERE btrim(account_subject) <> '';

CREATE TABLE workspace_model_routes (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    position integer NOT NULL CHECK (position >= 0),
    enabled boolean NOT NULL DEFAULT true,
    provider_type text NOT NULL CHECK (provider_type IN ('openai_compatible', 'chatgpt', 'grok')),
    credential_id uuid NULL,
    provider_base_url text NOT NULL DEFAULT '',
    model text NOT NULL CHECK (btrim(model) <> ''),
    api_key_env text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, position),
    UNIQUE (workspace_id, id),
    CONSTRAINT workspace_model_routes_credential_fk
        FOREIGN KEY (workspace_id, credential_id)
        REFERENCES model_oauth_credentials(workspace_id, id)
        ON DELETE RESTRICT,
    CONSTRAINT workspace_model_routes_provider_ck CHECK (
        (
            provider_type = 'openai_compatible'
            AND credential_id IS NULL
            AND btrim(provider_base_url) <> ''
        )
        OR
        (
            provider_type IN ('chatgpt', 'grok')
            AND btrim(provider_base_url) = ''
            AND btrim(api_key_env) = ''
            AND (NOT enabled OR credential_id IS NOT NULL)
        )
    )
);

CREATE INDEX workspace_model_routes_enabled_idx
    ON workspace_model_routes(workspace_id, position)
    WHERE enabled;

-- The former single-provider settings become route 0. Migration 000008 has
-- already removed the global enable/timeout/tool-round settings, so a
-- configured model is runnable by default and route.enabled is the only switch.
INSERT INTO workspace_model_routes (
    id, workspace_id, position, enabled, provider_type, credential_id,
    provider_base_url, model, api_key_env
)
SELECT
    md5(s.workspace_id::text || ':legacy-model-route')::uuid,
    s.workspace_id,
    0,
    CASE
        WHEN s.provider_type IN ('chatgpt', 'grok') THEN c.id IS NOT NULL
        ELSE true
    END,
    s.provider_type,
    c.id,
    CASE WHEN s.provider_type = 'openai_compatible' THEN s.provider_base_url ELSE '' END,
    s.model,
    CASE WHEN s.provider_type = 'openai_compatible' THEN s.api_key_env ELSE '' END
FROM workspace_agent_settings s
LEFT JOIN model_oauth_credentials c
    ON c.workspace_id = s.workspace_id
   AND c.provider = s.provider_type
WHERE btrim(s.model) <> ''
  AND (
      s.provider_type <> 'openai_compatible'
      OR btrim(s.provider_base_url) <> ''
  );

ALTER TABLE workspace_agent_settings
    DROP COLUMN provider_type,
    DROP COLUMN provider_base_url,
    DROP COLUMN model,
    DROP COLUMN api_key_env;

-- +goose Down

ALTER TABLE workspace_agent_settings
    ADD COLUMN provider_base_url text NOT NULL DEFAULT '',
    ADD COLUMN model text NOT NULL DEFAULT '',
    ADD COLUMN api_key_env text NOT NULL DEFAULT '',
    ADD COLUMN provider_type text NOT NULL DEFAULT 'openai_compatible'
        CHECK (provider_type IN ('openai_compatible', 'chatgpt', 'grok'));

UPDATE workspace_agent_settings s
SET provider_type = COALESCE((
        SELECT r.provider_type
        FROM workspace_model_routes r
        WHERE r.workspace_id = s.workspace_id
        ORDER BY r.position, r.id
        LIMIT 1
    ), 'openai_compatible'),
    provider_base_url = COALESCE((
        SELECT r.provider_base_url
        FROM workspace_model_routes r
        WHERE r.workspace_id = s.workspace_id
        ORDER BY r.position, r.id
        LIMIT 1
    ), ''),
    model = COALESCE((
        SELECT r.model
        FROM workspace_model_routes r
        WHERE r.workspace_id = s.workspace_id
        ORDER BY r.position, r.id
        LIMIT 1
    ), ''),
    api_key_env = COALESCE((
        SELECT r.api_key_env
        FROM workspace_model_routes r
        WHERE r.workspace_id = s.workspace_id
        ORDER BY r.position, r.id
        LIMIT 1
    ), '');

DROP TABLE workspace_model_routes;

-- v8/v7 can represent only one credential per provider. Preserve the newest
-- credential before restoring the old composite primary key.
DELETE FROM model_oauth_credentials older
USING model_oauth_credentials newer
WHERE older.workspace_id = newer.workspace_id
  AND older.provider = newer.provider
  AND (
      older.updated_at < newer.updated_at
      OR (older.updated_at = newer.updated_at AND older.id::text < newer.id::text)
  );

DROP INDEX model_oauth_credentials_account_identity_idx;

ALTER TABLE model_oauth_credentials
    DROP CONSTRAINT model_oauth_credentials_pkey,
    DROP CONSTRAINT model_oauth_credentials_workspace_id_id_key,
    DROP COLUMN id,
    DROP COLUMN account_subject,
    ADD PRIMARY KEY (workspace_id, provider);
