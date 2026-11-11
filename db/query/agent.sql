-- name: GetWorkspaceAgentSettings :one
SELECT *
FROM workspace_agent_settings
WHERE workspace_id = sqlc.arg(workspace_id);

-- name: UpsertWorkspaceAgentSettings :one
INSERT INTO workspace_agent_settings (
    workspace_id, source_id, system_instructions,
    default_model_route_id, discussion_model_route_id, plan_model_route_id,
    tool_guard_mode, tool_guard_model_route_id, tool_guard_timeout_seconds, tool_guard_custom_rules
)
VALUES (
    sqlc.arg(workspace_id), sqlc.arg(source_id), sqlc.arg(system_instructions),
    sqlc.narg(default_model_route_id), sqlc.narg(discussion_model_route_id), sqlc.narg(plan_model_route_id),
    sqlc.arg(tool_guard_mode), sqlc.narg(tool_guard_model_route_id), sqlc.arg(tool_guard_timeout_seconds), sqlc.arg(tool_guard_custom_rules)
)
ON CONFLICT (workspace_id) DO UPDATE
SET system_instructions = EXCLUDED.system_instructions,
    default_model_route_id = EXCLUDED.default_model_route_id,
    discussion_model_route_id = EXCLUDED.discussion_model_route_id,
    plan_model_route_id = EXCLUDED.plan_model_route_id,
    tool_guard_mode = EXCLUDED.tool_guard_mode,
    tool_guard_model_route_id = EXCLUDED.tool_guard_model_route_id,
    tool_guard_timeout_seconds = EXCLUDED.tool_guard_timeout_seconds,
    tool_guard_custom_rules = EXCLUDED.tool_guard_custom_rules,
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
