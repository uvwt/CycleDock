-- name: GetWorkspaceAgentSettings :one
SELECT *
FROM workspace_agent_settings
WHERE workspace_id = sqlc.arg(workspace_id);

-- name: UpsertWorkspaceAgentSettings :one
INSERT INTO workspace_agent_settings (
    workspace_id, source_id, system_instructions
)
VALUES (
    sqlc.arg(workspace_id), sqlc.arg(source_id), sqlc.arg(system_instructions)
)
ON CONFLICT (workspace_id) DO UPDATE
SET system_instructions = EXCLUDED.system_instructions,
    updated_at = now()
RETURNING *;

-- name: ListWorkspaceModelRoutes :many
SELECT *
FROM workspace_model_routes
WHERE workspace_id = sqlc.arg(workspace_id)
ORDER BY position, id;

-- name: DeleteWorkspaceModelRoutes :exec
DELETE FROM workspace_model_routes
WHERE workspace_id = sqlc.arg(workspace_id);

-- name: CreateWorkspaceModelRoute :one
INSERT INTO workspace_model_routes (
    id, workspace_id, position, enabled, provider_type, credential_id,
    provider_base_url, model, api_key_env
)
VALUES (
    sqlc.arg(id), sqlc.arg(workspace_id), sqlc.arg(position), sqlc.arg(enabled),
    sqlc.arg(provider_type), sqlc.narg(credential_id),
    sqlc.arg(provider_base_url), sqlc.arg(model), sqlc.arg(api_key_env)
)
RETURNING *;

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
