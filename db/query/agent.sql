-- name: GetWorkspaceAgentSettings :one
SELECT *
FROM workspace_agent_settings
WHERE workspace_id = sqlc.arg(workspace_id);

-- name: UpsertWorkspaceAgentSettings :one
INSERT INTO workspace_agent_settings (
    workspace_id, source_id, system_instructions,
    default_model_route_id, discussion_model_route_id, plan_model_route_id
)
VALUES (
    sqlc.arg(workspace_id), sqlc.arg(source_id), sqlc.arg(system_instructions),
    sqlc.narg(default_model_route_id), sqlc.narg(discussion_model_route_id), sqlc.narg(plan_model_route_id)
)
ON CONFLICT (workspace_id) DO UPDATE
SET system_instructions = EXCLUDED.system_instructions,
    default_model_route_id = EXCLUDED.default_model_route_id,
    discussion_model_route_id = EXCLUDED.discussion_model_route_id,
    plan_model_route_id = EXCLUDED.plan_model_route_id,
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
    provider_base_url, model, base_model, api_key_env, api_key_ciphertext,
    context_window_tokens, max_input_tokens, max_output_tokens,
    limits_source, limits_verified_at
)
VALUES (
    sqlc.arg(id), sqlc.arg(workspace_id), sqlc.arg(position), sqlc.arg(enabled),
    sqlc.arg(provider_type), sqlc.narg(credential_id),
    sqlc.arg(provider_base_url), sqlc.arg(model), sqlc.arg(base_model), sqlc.arg(api_key_env),
    sqlc.narg(api_key_ciphertext),
    sqlc.narg(context_window_tokens), sqlc.narg(max_input_tokens), sqlc.narg(max_output_tokens),
    COALESCE(NULLIF(btrim(sqlc.arg(limits_source)::text), ''), 'unknown'), sqlc.narg(limits_verified_at)
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
    id, workspace_id, name, url, bearer_token_ciphertext, enabled, timeout_ms
)
VALUES (
    sqlc.arg(id), sqlc.arg(workspace_id), sqlc.arg(name), sqlc.arg(url),
    sqlc.arg(bearer_token_ciphertext), sqlc.arg(enabled), sqlc.arg(timeout_ms)
)
ON CONFLICT (workspace_id, id) DO UPDATE
SET name = EXCLUDED.name,
    url = EXCLUDED.url,
    bearer_token_ciphertext = CASE
        WHEN sqlc.arg(replace_bearer_token)::boolean THEN EXCLUDED.bearer_token_ciphertext
        ELSE workspace_mcp_connections.bearer_token_ciphertext
    END,
    enabled = EXCLUDED.enabled,
    timeout_ms = EXCLUDED.timeout_ms,
    updated_at = now()
WHERE workspace_mcp_connections.url = EXCLUDED.url
   OR workspace_mcp_connections.bearer_token_ciphertext IS NULL
   OR sqlc.arg(replace_bearer_token)::boolean
RETURNING *;

-- name: DeleteWorkspaceMCPConnection :execrows
DELETE FROM workspace_mcp_connections
WHERE workspace_id = sqlc.arg(workspace_id)
  AND id = sqlc.arg(id);
