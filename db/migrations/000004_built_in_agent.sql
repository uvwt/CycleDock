-- +goose Up

CREATE TABLE workspace_agent_settings (
    workspace_id uuid PRIMARY KEY REFERENCES workspaces(id) ON DELETE CASCADE,
    source_id uuid NOT NULL,
    enabled boolean NOT NULL DEFAULT false,
    provider_base_url text NOT NULL DEFAULT '',
    model text NOT NULL DEFAULT '',
    api_key_env text NOT NULL DEFAULT '',
    request_timeout_ms integer NOT NULL DEFAULT 120000 CHECK (request_timeout_ms BETWEEN 1000 AND 600000),
    max_tool_rounds integer NOT NULL DEFAULT 12 CHECK (max_tool_rounds BETWEEN 1 AND 32),
    system_instructions text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, source_id),
    UNIQUE (source_id),
    CONSTRAINT workspace_agent_settings_source_fk
        FOREIGN KEY (workspace_id, source_id)
        REFERENCES sources(workspace_id, id)
        ON DELETE RESTRICT,
    CONSTRAINT workspace_agent_settings_enabled_ck CHECK (
        NOT enabled OR (btrim(provider_base_url) <> '' AND btrim(model) <> '')
    )
);

CREATE TABLE workspace_mcp_connections (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    name text NOT NULL CHECK (btrim(name) <> ''),
    url text NOT NULL CHECK (btrim(url) <> ''),
    bearer_token_env text NOT NULL DEFAULT '',
    enabled boolean NOT NULL DEFAULT true,
    timeout_ms integer NOT NULL DEFAULT 30000 CHECK (timeout_ms BETWEEN 1000 AND 300000),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, name)
);

CREATE INDEX workspace_agent_settings_enabled_idx
    ON workspace_agent_settings(workspace_id)
    WHERE enabled;

CREATE INDEX workspace_mcp_connections_enabled_idx
    ON workspace_mcp_connections(workspace_id, name)
    WHERE enabled;

-- +goose Down

DROP TABLE IF EXISTS workspace_mcp_connections;
DROP TABLE IF EXISTS workspace_agent_settings;
