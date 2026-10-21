-- name: CountWorkspaces :one
SELECT count(*) FROM workspaces;

-- name: GetFirstWorkspace :one
SELECT *
FROM workspaces
ORDER BY created_at, id
LIMIT 1;

-- name: GetLocalAdminIdentity :one
SELECT
    ha.id AS human_account_id,
    ha.display_name,
    ha.email_snapshot,
    wm.workspace_id,
    wm.source_id,
    wm.role,
    w.name AS workspace_name,
    s.name AS source_name,
    s.kind AS source_kind
FROM human_accounts ha
JOIN workspace_memberships wm
  ON wm.human_account_id = ha.id
JOIN workspaces w
  ON w.id = wm.workspace_id
JOIN sources s
  ON s.workspace_id = wm.workspace_id
 AND s.id = wm.source_id
WHERE ha.is_local_admin
LIMIT 1;

-- name: GetOIDCPersonalIdentity :one
SELECT
    ha.id AS human_account_id,
    ha.display_name,
    ha.email_snapshot,
    wm.workspace_id,
    wm.source_id,
    wm.role,
    w.name AS workspace_name,
    s.name AS source_name,
    s.kind AS source_kind
FROM oidc_identities oi
JOIN human_accounts ha
  ON ha.id = oi.human_account_id
JOIN workspace_memberships wm
  ON wm.human_account_id = ha.id
 AND wm.is_personal
JOIN workspaces w
  ON w.id = wm.workspace_id
JOIN sources s
  ON s.workspace_id = wm.workspace_id
 AND s.id = wm.source_id
WHERE oi.issuer = sqlc.arg(issuer)
  AND oi.subject = sqlc.arg(subject);

-- name: CreateHumanAccount :one
INSERT INTO human_accounts (
    id, display_name, email_snapshot, is_local_admin
)
VALUES (
    sqlc.arg(id), sqlc.arg(display_name), sqlc.narg(email_snapshot), sqlc.arg(is_local_admin)
)
RETURNING *;

-- name: UpdateHumanAccountSnapshot :exec
UPDATE human_accounts
SET display_name = sqlc.arg(display_name),
    email_snapshot = sqlc.narg(email_snapshot),
    updated_at = now()
WHERE id = sqlc.arg(id);

-- name: UpdateHumanSourceName :exec
UPDATE sources
SET name = sqlc.arg(name),
    updated_at = now()
WHERE workspace_id = sqlc.arg(workspace_id)
  AND id = sqlc.arg(id);

-- name: CreateOIDCIdentity :exec
INSERT INTO oidc_identities (issuer, subject, human_account_id)
VALUES (sqlc.arg(issuer), sqlc.arg(subject), sqlc.arg(human_account_id));

-- name: CreateWorkspaceMembership :exec
INSERT INTO workspace_memberships (
    workspace_id, human_account_id, source_id, role, is_personal
)
VALUES (
    sqlc.arg(workspace_id), sqlc.arg(human_account_id), sqlc.arg(source_id),
    sqlc.arg(role), sqlc.arg(is_personal)
);

-- name: CreateWebSession :exec
INSERT INTO web_sessions (
    id, human_account_id, workspace_id, source_id, token_digest, expires_at
)
VALUES (
    sqlc.arg(id), sqlc.arg(human_account_id), sqlc.arg(workspace_id),
    sqlc.arg(source_id), sqlc.arg(token_digest), sqlc.arg(expires_at)
);

-- name: GetWebSessionIdentity :one
SELECT
    ws.human_account_id,
    ws.workspace_id,
    ws.source_id,
    ha.display_name,
    ha.email_snapshot,
    wm.role,
    w.name AS workspace_name,
    s.name AS source_name,
    s.kind AS source_kind,
    ws.expires_at
FROM web_sessions ws
JOIN human_accounts ha
  ON ha.id = ws.human_account_id
JOIN workspace_memberships wm
  ON wm.workspace_id = ws.workspace_id
 AND wm.human_account_id = ws.human_account_id
 AND wm.source_id = ws.source_id
JOIN workspaces w
  ON w.id = ws.workspace_id
JOIN sources s
  ON s.workspace_id = ws.workspace_id
 AND s.id = ws.source_id
WHERE ws.token_digest = sqlc.arg(token_digest)
  AND ws.expires_at > now();

-- name: ListHumanWorkspaces :many
SELECT
    wm.workspace_id,
    wm.source_id,
    wm.role,
    wm.is_personal,
    w.name AS workspace_name
FROM workspace_memberships wm
JOIN workspaces w ON w.id = wm.workspace_id
WHERE wm.human_account_id = sqlc.arg(human_account_id)
ORDER BY wm.is_personal DESC, w.created_at, w.id;

-- name: SwitchWebSessionWorkspace :execrows
UPDATE web_sessions ws
SET workspace_id = membership.workspace_id,
    source_id = membership.source_id
FROM workspace_memberships membership
WHERE ws.token_digest = sqlc.arg(token_digest)
  AND ws.expires_at > now()
  AND membership.human_account_id = ws.human_account_id
  AND membership.workspace_id = sqlc.arg(workspace_id);

-- name: DeleteWebSession :exec
DELETE FROM web_sessions
WHERE token_digest = sqlc.arg(token_digest);

-- name: DeleteExpiredWebSessions :exec
DELETE FROM web_sessions
WHERE expires_at <= now();

-- name: CreateWebAuthFlow :exec
INSERT INTO web_auth_flows (
    state_digest, provider, code_verifier, nonce, expires_at
)
VALUES (
    sqlc.arg(state_digest), 'oidc', sqlc.arg(code_verifier),
    sqlc.arg(nonce), sqlc.arg(expires_at)
);

-- name: ConsumeWebAuthFlow :one
UPDATE web_auth_flows
SET consumed_at = now()
WHERE state_digest = sqlc.arg(state_digest)
  AND provider = 'oidc'
  AND consumed_at IS NULL
  AND expires_at > now()
RETURNING *;

-- name: DeleteExpiredWebAuthFlows :exec
DELETE FROM web_auth_flows
WHERE expires_at <= now();
