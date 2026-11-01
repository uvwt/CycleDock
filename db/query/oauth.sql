-- name: CreateOAuthClient :one
INSERT INTO oauth_clients (client_id, client_name, redirect_uris)
VALUES (sqlc.arg(client_id), sqlc.arg(client_name), sqlc.arg(redirect_uris))
RETURNING *;

-- name: GetOAuthClient :one
SELECT *
FROM oauth_clients
WHERE client_id = sqlc.arg(client_id);

-- name: ListAuthorizedOAuthClients :many
SELECT
    g.client_id,
    c.client_name,
    g.source_id,
    s.name AS source_name,
    g.updated_at AS authorized_at
FROM oauth_grants g
JOIN oauth_clients c ON c.client_id = g.client_id
JOIN sources s
  ON s.workspace_id = g.workspace_id
 AND s.id = g.source_id
WHERE g.workspace_id = sqlc.arg(workspace_id)
  AND g.human_account_id = sqlc.arg(human_account_id)
  AND g.revoked_at IS NULL
  AND s.kind = 'oauth_client'
ORDER BY g.updated_at DESC, g.client_id;

-- name: GetOAuthHumanMembership :one
SELECT
    m.workspace_id,
    m.human_account_id,
    m.source_id,
    m.role,
    s.kind AS source_kind
FROM workspace_memberships m
JOIN sources s
  ON s.workspace_id = m.workspace_id
 AND s.id = m.source_id
WHERE m.workspace_id = sqlc.arg(workspace_id)
  AND m.human_account_id = sqlc.arg(human_account_id)
  AND m.source_id = sqlc.arg(source_id);

-- name: UpdateOAuthSourceName :exec
UPDATE sources
SET name = sqlc.arg(name),
    updated_at = now()
WHERE workspace_id = sqlc.arg(workspace_id)
  AND id = sqlc.arg(id)
  AND kind = 'oauth_client';

-- name: GetOAuthGrantForUpdate :one
SELECT
    g.*,
    s.kind AS source_kind
FROM oauth_grants g
JOIN sources s
  ON s.workspace_id = g.workspace_id
 AND s.id = g.source_id
WHERE g.workspace_id = sqlc.arg(workspace_id)
  AND g.client_id = sqlc.arg(client_id)
  AND g.resource = sqlc.arg(resource)
FOR UPDATE OF g;

-- name: CreateOAuthGrant :one
INSERT INTO oauth_grants (
    id, workspace_id, human_account_id, human_source_id,
    client_id, source_id, resource, scope
)
VALUES (
    sqlc.arg(id), sqlc.arg(workspace_id), sqlc.arg(human_account_id), sqlc.arg(human_source_id),
    sqlc.arg(client_id), sqlc.arg(source_id), sqlc.arg(resource), sqlc.arg(scope)
)
RETURNING *;

-- name: RefreshOAuthGrantApproval :one
UPDATE oauth_grants
SET human_account_id = sqlc.arg(human_account_id),
    human_source_id = sqlc.arg(human_source_id),
    scope = sqlc.arg(scope),
    revoked_at = NULL,
    updated_at = now()
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: CreateOAuthAuthorizationCode :exec
INSERT INTO oauth_authorization_codes (
    code_digest, grant_id, redirect_uri, code_challenge, scope, resource, expires_at
)
VALUES (
    sqlc.arg(code_digest), sqlc.arg(grant_id), sqlc.arg(redirect_uri),
    sqlc.arg(code_challenge), sqlc.arg(scope), sqlc.arg(resource), sqlc.arg(expires_at)
);

-- name: GetOAuthAuthorizationCodeForUpdate :one
SELECT
    c.code_digest,
    c.grant_id,
    c.redirect_uri,
    c.code_challenge,
    c.scope,
    c.resource,
    c.expires_at,
    c.consumed_at,
    g.client_id,
    g.workspace_id,
    g.source_id,
    g.revoked_at AS grant_revoked_at
FROM oauth_authorization_codes c
JOIN oauth_grants g ON g.id = c.grant_id
WHERE c.code_digest = sqlc.arg(code_digest)
FOR UPDATE OF c;

-- name: ConsumeOAuthAuthorizationCode :execrows
UPDATE oauth_authorization_codes
SET consumed_at = now()
WHERE code_digest = sqlc.arg(code_digest)
  AND consumed_at IS NULL;

-- name: CreateOAuthToken :exec
INSERT INTO oauth_tokens (
    id, grant_id, family_id, access_token_digest, refresh_token_digest,
    access_expires_at, refresh_expires_at
)
VALUES (
    sqlc.arg(id), sqlc.arg(grant_id), sqlc.arg(family_id),
    sqlc.arg(access_token_digest), sqlc.arg(refresh_token_digest),
    sqlc.arg(access_expires_at), sqlc.arg(refresh_expires_at)
);

-- name: GetOAuthAccessTokenIdentity :one
SELECT
    g.workspace_id,
    g.source_id,
    g.client_id,
    c.client_name,
    g.scope,
    g.resource,
    t.access_expires_at
FROM oauth_tokens t
JOIN oauth_grants g ON g.id = t.grant_id
JOIN oauth_clients c ON c.client_id = g.client_id
JOIN sources s
  ON s.workspace_id = g.workspace_id
 AND s.id = g.source_id
WHERE t.access_token_digest = sqlc.arg(access_token_digest)
  AND t.revoked_at IS NULL
  AND g.revoked_at IS NULL
  AND s.kind = 'oauth_client'
  AND t.access_expires_at > now();

-- name: GetOAuthRefreshTokenForUpdate :one
SELECT
    t.id AS token_id,
    t.grant_id,
    t.family_id,
    t.refresh_expires_at,
    t.revoked_at AS token_revoked_at,
    g.workspace_id,
    g.source_id,
    g.client_id,
    g.scope,
    g.resource,
    g.revoked_at AS grant_revoked_at
FROM oauth_tokens t
JOIN oauth_grants g ON g.id = t.grant_id
WHERE t.refresh_token_digest = sqlc.arg(refresh_token_digest)
FOR UPDATE OF t;

-- name: RevokeOAuthToken :execrows
UPDATE oauth_tokens
SET revoked_at = now()
WHERE id = sqlc.arg(id)
  AND revoked_at IS NULL;

-- name: RevokeOAuthTokenFamily :exec
UPDATE oauth_tokens
SET revoked_at = COALESCE(revoked_at, now())
WHERE family_id = sqlc.arg(family_id);

-- name: DeleteExpiredOAuthArtifacts :exec
DELETE FROM oauth_authorization_codes
WHERE expires_at <= now()
   OR (consumed_at IS NOT NULL AND consumed_at <= now() - interval '1 day');
