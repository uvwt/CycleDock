-- +goose Up

DROP INDEX IF EXISTS workspace_agent_settings_enabled_idx;

ALTER TABLE workspace_agent_settings
    DROP CONSTRAINT IF EXISTS workspace_agent_settings_enabled_ck,
    DROP COLUMN enabled,
    DROP COLUMN request_timeout_ms,
    DROP COLUMN max_tool_rounds;

-- +goose Down

ALTER TABLE workspace_agent_settings
    ADD COLUMN enabled boolean NOT NULL DEFAULT false,
    ADD COLUMN request_timeout_ms integer NOT NULL DEFAULT 120000
        CHECK (request_timeout_ms BETWEEN 1000 AND 600000),
    ADD COLUMN max_tool_rounds integer NOT NULL DEFAULT 12
        CHECK (max_tool_rounds BETWEEN 1 AND 32);

UPDATE workspace_agent_settings
SET enabled = btrim(model) <> ''
    AND (
        provider_type <> 'openai_compatible'
        OR btrim(provider_base_url) <> ''
    );

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

CREATE INDEX workspace_agent_settings_enabled_idx
    ON workspace_agent_settings(workspace_id)
    WHERE enabled;
