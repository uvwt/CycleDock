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

-- name: ListWorkspaceAPICredentials :many
SELECT *
FROM workspace_api_credentials
WHERE workspace_id = sqlc.arg(workspace_id)
ORDER BY created_at ASC, id ASC;

-- name: GetWorkspaceAPICredential :one
SELECT *
FROM workspace_api_credentials
WHERE workspace_id = sqlc.arg(workspace_id)
  AND id = sqlc.arg(id);

-- name: UpsertWorkspaceAPICredential :one
INSERT INTO workspace_api_credentials (
    id, workspace_id, name, base_url, api_key_env, api_key_ciphertext
)
VALUES (
    sqlc.arg(id), sqlc.arg(workspace_id), sqlc.arg(name), sqlc.arg(base_url),
    sqlc.arg(api_key_env), sqlc.narg(api_key_ciphertext)
)
ON CONFLICT (workspace_id, id) DO UPDATE
SET name = EXCLUDED.name,
    base_url = EXCLUDED.base_url,
    api_key_env = EXCLUDED.api_key_env,
    api_key_ciphertext = CASE
        WHEN sqlc.arg(replace_api_key)::boolean THEN EXCLUDED.api_key_ciphertext
        ELSE workspace_api_credentials.api_key_ciphertext
    END,
    updated_at = now()
WHERE workspace_api_credentials.base_url = EXCLUDED.base_url
   OR workspace_api_credentials.api_key_ciphertext IS NULL
   OR sqlc.arg(replace_api_key)::boolean
RETURNING *;

-- name: DeleteWorkspaceAPICredential :execrows
DELETE FROM workspace_api_credentials
WHERE workspace_id = sqlc.arg(workspace_id)
  AND id = sqlc.arg(id);

-- name: ListWorkspaceModelRoutes :many
SELECT
    r.id,
    r.workspace_id,
    r.position,
    r.enabled,
    r.provider_type,
    r.credential_id,
    r.api_credential_id,
    COALESCE(ac.name, '')::text AS api_credential_name,
    COALESCE(ac.base_url, '')::text AS provider_base_url,
    r.model,
    r.base_model,
    COALESCE(ac.api_key_env, '')::text AS api_key_env,
    ac.api_key_ciphertext,
    r.context_window_tokens,
    r.max_input_tokens,
    r.max_output_tokens,
    r.limits_source,
    r.limits_verified_at,
    r.created_at,
    r.updated_at
FROM workspace_model_routes r
LEFT JOIN workspace_api_credentials ac
  ON ac.workspace_id = r.workspace_id
 AND ac.id = r.api_credential_id
WHERE r.workspace_id = sqlc.arg(workspace_id)
ORDER BY r.position, r.id;

-- name: DeleteRemovedWorkspaceModelRoutes :exec
DELETE FROM workspace_model_routes
WHERE workspace_id = sqlc.arg(workspace_id)
  AND NOT (id = ANY(sqlc.arg(keep_ids)::uuid[]));

-- name: ShiftWorkspaceModelRoutePositions :exec
UPDATE workspace_model_routes
SET position = position + 1000
WHERE workspace_id = sqlc.arg(workspace_id);

-- name: UpsertWorkspaceModelRoute :one
INSERT INTO workspace_model_routes (
    id, workspace_id, position, enabled, provider_type, credential_id, api_credential_id,
    model, base_model,
    context_window_tokens, max_input_tokens, max_output_tokens,
    limits_source, limits_verified_at
)
VALUES (
    sqlc.arg(id), sqlc.arg(workspace_id), sqlc.arg(position), sqlc.arg(enabled),
    sqlc.arg(provider_type), sqlc.narg(credential_id), sqlc.narg(api_credential_id),
    sqlc.arg(model), sqlc.arg(base_model),
    sqlc.narg(context_window_tokens), sqlc.narg(max_input_tokens), sqlc.narg(max_output_tokens),
    COALESCE(NULLIF(btrim(sqlc.arg(limits_source)::text), ''), 'unknown'), sqlc.narg(limits_verified_at)
)
ON CONFLICT (id) DO UPDATE
SET position = EXCLUDED.position,
    enabled = EXCLUDED.enabled,
    provider_type = EXCLUDED.provider_type,
    credential_id = EXCLUDED.credential_id,
    api_credential_id = EXCLUDED.api_credential_id,
    model = EXCLUDED.model,
    base_model = EXCLUDED.base_model,
    context_window_tokens = EXCLUDED.context_window_tokens,
    max_input_tokens = EXCLUDED.max_input_tokens,
    max_output_tokens = EXCLUDED.max_output_tokens,
    limits_source = EXCLUDED.limits_source,
    limits_verified_at = EXCLUDED.limits_verified_at,
    updated_at = now()
WHERE workspace_model_routes.workspace_id = EXCLUDED.workspace_id
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
    id, workspace_id, name, url, bearer_token_ciphertext, enabled, timeout_ms,
    tool_guard_mode, tool_guard_model_route_id, tool_guard_timeout_seconds, tool_guard_custom_rules
)
VALUES (
    sqlc.arg(id), sqlc.arg(workspace_id), sqlc.arg(name), sqlc.arg(url),
    sqlc.arg(bearer_token_ciphertext), sqlc.arg(enabled), sqlc.arg(timeout_ms),
    sqlc.arg(tool_guard_mode), sqlc.narg(tool_guard_model_route_id), sqlc.arg(tool_guard_timeout_seconds), sqlc.arg(tool_guard_custom_rules)
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
    tool_guard_mode = EXCLUDED.tool_guard_mode,
    tool_guard_model_route_id = EXCLUDED.tool_guard_model_route_id,
    tool_guard_timeout_seconds = EXCLUDED.tool_guard_timeout_seconds,
    tool_guard_custom_rules = EXCLUDED.tool_guard_custom_rules,
    updated_at = now()
WHERE workspace_mcp_connections.url = EXCLUDED.url
   OR workspace_mcp_connections.bearer_token_ciphertext IS NULL
   OR sqlc.arg(replace_bearer_token)::boolean
RETURNING *;

-- name: DeleteWorkspaceMCPConnection :execrows
DELETE FROM workspace_mcp_connections
WHERE workspace_id = sqlc.arg(workspace_id)
  AND id = sqlc.arg(id);

-- name: RecordAgentModelCall :one
INSERT INTO agent_model_calls (
    id, workspace_id, card_id, turn_id, attempt_id, invocation_id,
    call_index, purpose, model_route_id, model_id, provider,
    started_at, finished_at, duration_ms, status,
    input_tokens, output_tokens, cached_input_tokens, reasoning_output_tokens,
    usage_source, error_code, error_summary
)
VALUES (
    sqlc.arg(id), sqlc.arg(workspace_id), sqlc.arg(card_id), sqlc.arg(turn_id), sqlc.arg(attempt_id), sqlc.arg(invocation_id),
    sqlc.arg(call_index), sqlc.arg(purpose), sqlc.narg(model_route_id), sqlc.arg(model_id), sqlc.arg(provider),
    sqlc.arg(started_at), sqlc.arg(finished_at), sqlc.arg(duration_ms), sqlc.arg(status),
    sqlc.arg(input_tokens), sqlc.arg(output_tokens), sqlc.arg(cached_input_tokens), sqlc.arg(reasoning_output_tokens),
    sqlc.arg(usage_source), sqlc.arg(error_code), sqlc.arg(error_summary)
)
RETURNING *;

-- name: ListAttemptModelCalls :many
SELECT *
FROM agent_model_calls
WHERE workspace_id = sqlc.arg(workspace_id)
  AND card_id = sqlc.arg(card_id)
  AND attempt_id = sqlc.arg(attempt_id)
ORDER BY call_index ASC, started_at ASC;

-- name: CreateCardToolApproval :one
INSERT INTO card_tool_approvals (
    id, workspace_id, card_id, turn_id, attempt_id,
    tool_call_id, tool_name, arguments, risk_level,
    reversible, reason, status
)
VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8, $9,
    $10, $11, 'pending'
)
RETURNING *;

-- name: GetCardToolApproval :one
SELECT *
FROM card_tool_approvals
WHERE workspace_id = $1 AND card_id = $2 AND id = $3;

-- name: GetCardToolApprovalByID :one
SELECT *
FROM card_tool_approvals
WHERE workspace_id = $1 AND id = $2;

-- name: ResolveCardToolApproval :one
UPDATE card_tool_approvals
SET status = $4,
    resolution_comment = $5,
    resolved_by_source_id = $6,
    resolved_at = now(),
    updated_at = now()
WHERE workspace_id = $1 AND card_id = $2 AND id = $3 AND status = 'pending'
RETURNING *;

-- name: ListCardToolApprovals :many
SELECT *
FROM card_tool_approvals
WHERE workspace_id = $1 AND card_id = $2
ORDER BY created_at DESC;

-- name: CreateCardToolWhitelist :one
INSERT INTO card_tool_whitelists (
    id, workspace_id, card_id, tool_name, pattern, created_by_source_id
)
VALUES (
    $1, $2, $3, $4, $5, $6
)
RETURNING *;

-- name: CheckCardToolWhitelisted :one
SELECT EXISTS (
    SELECT 1 FROM card_tool_whitelists
    WHERE workspace_id = $1 AND card_id = $2 AND tool_name = $3
) AS whitelisted;

-- name: GetWorkspaceDeviceToolGuard :one
SELECT *
FROM workspace_device_tool_guards
WHERE workspace_id = sqlc.arg(workspace_id)
  AND device_node_id = sqlc.arg(device_node_id);

-- name: ListWorkspaceDeviceToolGuards :many
SELECT *
FROM workspace_device_tool_guards
WHERE workspace_id = sqlc.arg(workspace_id)
ORDER BY device_name, device_node_id;

-- name: UpsertWorkspaceDeviceToolGuard :one
INSERT INTO workspace_device_tool_guards (
    id, workspace_id, device_node_id, device_name, mode,
    model_route_id, timeout_seconds, custom_rules
)
VALUES (
    sqlc.arg(id), sqlc.arg(workspace_id), sqlc.arg(device_node_id), sqlc.arg(device_name), sqlc.arg(mode),
    sqlc.narg(model_route_id), sqlc.arg(timeout_seconds), sqlc.arg(custom_rules)
)
ON CONFLICT (workspace_id, device_node_id) DO UPDATE
SET device_name = EXCLUDED.device_name,
    mode = EXCLUDED.mode,
    model_route_id = EXCLUDED.model_route_id,
    timeout_seconds = EXCLUDED.timeout_seconds,
    custom_rules = EXCLUDED.custom_rules,
    updated_at = now()
RETURNING *;

-- name: DeleteWorkspaceDeviceToolGuard :execrows
DELETE FROM workspace_device_tool_guards
WHERE workspace_id = sqlc.arg(workspace_id)
  AND device_node_id = sqlc.arg(device_node_id);
