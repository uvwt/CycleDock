-- name: GetNexusDockOAuthClient :one
SELECT * FROM nexusdock_oauth_clients
WHERE resource = sqlc.arg(resource);

-- name: CreateNexusDockOAuthClient :one
INSERT INTO nexusdock_oauth_clients (resource, client_id)
VALUES (sqlc.arg(resource), sqlc.arg(client_id))
ON CONFLICT (resource) DO UPDATE
SET client_id = nexusdock_oauth_clients.client_id
RETURNING *;

-- name: DeleteExpiredNexusDockAuthFlows :exec
DELETE FROM nexusdock_auth_flows
WHERE expires_at <= now() OR consumed_at IS NOT NULL;

-- name: CreateNexusDockAuthFlow :exec
INSERT INTO nexusdock_auth_flows (
    state_digest, workspace_id, human_account_id, source_id,
    client_id, code_verifier, expires_at
)
VALUES (
    sqlc.arg(state_digest), sqlc.arg(workspace_id), sqlc.arg(human_account_id),
    sqlc.arg(source_id), sqlc.arg(client_id), sqlc.arg(code_verifier), sqlc.arg(expires_at)
);

-- name: ConsumeNexusDockAuthFlow :one
UPDATE nexusdock_auth_flows
SET consumed_at = now()
WHERE state_digest = sqlc.arg(state_digest)
  AND workspace_id = sqlc.arg(workspace_id)
  AND human_account_id = sqlc.arg(human_account_id)
  AND source_id = sqlc.arg(source_id)
  AND consumed_at IS NULL
  AND expires_at > now()
RETURNING *;

-- name: GetWorkspaceNexusDockConnection :one
SELECT * FROM workspace_nexusdock_connections
WHERE workspace_id = sqlc.arg(workspace_id);

-- name: UpsertWorkspaceNexusDockConnection :one
INSERT INTO workspace_nexusdock_connections (
    workspace_id, resource, client_id,
    access_token_ciphertext, refresh_token_ciphertext, access_expires_at
)
VALUES (
    sqlc.arg(workspace_id), sqlc.arg(resource), sqlc.arg(client_id),
    sqlc.arg(access_token_ciphertext), sqlc.arg(refresh_token_ciphertext),
    sqlc.arg(access_expires_at)
)
ON CONFLICT (workspace_id) DO UPDATE
SET resource = EXCLUDED.resource,
    client_id = EXCLUDED.client_id,
    access_token_ciphertext = EXCLUDED.access_token_ciphertext,
    refresh_token_ciphertext = EXCLUDED.refresh_token_ciphertext,
    access_expires_at = EXCLUDED.access_expires_at,
    updated_at = now()
RETURNING *;

-- name: DeleteWorkspaceNexusDockConnection :execrows
DELETE FROM workspace_nexusdock_connections
WHERE workspace_id = sqlc.arg(workspace_id);
