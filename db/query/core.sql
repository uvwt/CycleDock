-- name: CreateWorkspace :one
INSERT INTO workspaces (id, name)
VALUES ($1, $2)
RETURNING *;

-- name: CreateSource :one
INSERT INTO sources (id, workspace_id, kind, name)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: CreateProject :one
INSERT INTO projects (id, workspace_id, name, description, instructions)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: CreateCard :one
INSERT INTO cards (
    id, workspace_id, project_id, kind, title, description,
    discussion_state, todo_state, ongoing_state, source_discussion_id,
    required_confirmations, created_by_source_id
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
RETURNING *;

-- name: GetCard :one
SELECT *
FROM cards
WHERE workspace_id = $1 AND id = $2;

-- name: GetSource :one
SELECT *
FROM sources
WHERE workspace_id = $1 AND id = $2;

-- name: ListCardEvents :many
SELECT *
FROM events
WHERE workspace_id = $1 AND card_id = $2
ORDER BY sequence;

-- name: LatestCheckin :one
SELECT *
FROM events
WHERE workspace_id = $1
  AND card_id = $2
  AND event_type = 'checkin'
ORDER BY sequence DESC
LIMIT 1;
