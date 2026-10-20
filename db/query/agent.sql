-- name: GetWorkspaceAgentSettings :one
SELECT *
FROM workspace_agent_settings
WHERE workspace_id = sqlc.arg(workspace_id);

-- name: UpsertWorkspaceAgentSettings :one
INSERT INTO workspace_agent_settings (
    workspace_id, source_id, enabled, provider_type, provider_base_url, model,
    api_key_env, request_timeout_ms, max_tool_rounds, system_instructions
)
VALUES (
    sqlc.arg(workspace_id), sqlc.arg(source_id), sqlc.arg(enabled),
    sqlc.arg(provider_type), sqlc.arg(provider_base_url), sqlc.arg(model), sqlc.arg(api_key_env),
    sqlc.arg(request_timeout_ms), sqlc.arg(max_tool_rounds), sqlc.arg(system_instructions)
)
ON CONFLICT (workspace_id) DO UPDATE
SET enabled = EXCLUDED.enabled,
    provider_type = EXCLUDED.provider_type,
    provider_base_url = EXCLUDED.provider_base_url,
    model = EXCLUDED.model,
    api_key_env = EXCLUDED.api_key_env,
    request_timeout_ms = EXCLUDED.request_timeout_ms,
    max_tool_rounds = EXCLUDED.max_tool_rounds,
    system_instructions = EXCLUDED.system_instructions,
    updated_at = now()
RETURNING *;

-- name: ListEnabledWorkspaceAgentSettings :many
SELECT *
FROM workspace_agent_settings
WHERE enabled
ORDER BY workspace_id;

-- name: ListWorkspaceMCPConnections :many
SELECT *
FROM workspace_mcp_connections
WHERE workspace_id = sqlc.arg(workspace_id)
ORDER BY name, id;

-- name: ListEnabledWorkspaceMCPConnections :many
SELECT *
FROM workspace_mcp_connections
WHERE workspace_id = sqlc.arg(workspace_id)
  AND enabled
ORDER BY name, id;

-- name: GetWorkspaceMCPConnection :one
SELECT *
FROM workspace_mcp_connections
WHERE workspace_id = sqlc.arg(workspace_id)
  AND id = sqlc.arg(id);

-- name: UpsertWorkspaceMCPConnection :one
INSERT INTO workspace_mcp_connections (
    id, workspace_id, name, url, bearer_token_env, enabled, timeout_ms
)
VALUES (
    sqlc.arg(id), sqlc.arg(workspace_id), sqlc.arg(name), sqlc.arg(url),
    sqlc.arg(bearer_token_env), sqlc.arg(enabled), sqlc.arg(timeout_ms)
)
ON CONFLICT (workspace_id, id) DO UPDATE
SET name = EXCLUDED.name,
    url = EXCLUDED.url,
    bearer_token_env = EXCLUDED.bearer_token_env,
    enabled = EXCLUDED.enabled,
    timeout_ms = EXCLUDED.timeout_ms,
    updated_at = now()
RETURNING *;

-- name: DeleteWorkspaceMCPConnection :execrows
DELETE FROM workspace_mcp_connections
WHERE workspace_id = sqlc.arg(workspace_id)
  AND id = sqlc.arg(id);
