-- name: ListWorkspaceCardSummaries :many
SELECT
    c.*,
    p.name AS project_name,
    COALESCE(latest_checkin.checkin_status, 'unknown') AS health,
    current_attempt.status AS current_attempt_status,
    (
        SELECT count(*)
        FROM card_turns t
        WHERE t.workspace_id = c.workspace_id
          AND t.card_id = c.id
          AND t.cancelled_at IS NULL
          AND NOT EXISTS (
              SELECT 1 FROM turn_attempts a
              WHERE a.workspace_id = t.workspace_id
                AND a.turn_id = t.id
          )
    ) AS pending_turn_count
FROM cards c
JOIN projects p
  ON p.workspace_id = c.workspace_id
 AND p.id = c.project_id
LEFT JOIN LATERAL (
    SELECT e.checkin_status
    FROM events e
    WHERE e.workspace_id = c.workspace_id
      AND e.card_id = c.id
      AND e.event_type = 'checkin'
    ORDER BY e.sequence DESC
    LIMIT 1
) latest_checkin ON true
LEFT JOIN turn_attempts current_attempt
  ON current_attempt.workspace_id = c.workspace_id
 AND current_attempt.card_id = c.id
 AND current_attempt.status IN ('queued', 'running', 'waiting_human')
WHERE c.workspace_id = sqlc.arg(workspace_id)
ORDER BY p.created_at, c.created_at, c.id;

-- name: GetCreatedCardRequest :one
SELECT *
FROM cards
WHERE workspace_id = sqlc.arg(workspace_id)
  AND created_by_source_id = sqlc.arg(created_by_source_id)
  AND create_idempotency_key = sqlc.arg(create_idempotency_key);

-- name: CreateAgentCard :one
INSERT INTO cards (
    id, workspace_id, project_id, kind, title, description,
    discussion_state, todo_state, ongoing_state, source_discussion_id,
    required_confirmations, created_by_source_id,
    create_idempotency_key, create_request_hash
)
VALUES (
    sqlc.arg(id), sqlc.arg(workspace_id), sqlc.arg(project_id), sqlc.arg(kind),
    sqlc.arg(title), sqlc.arg(description), sqlc.narg(discussion_state), sqlc.narg(todo_state),
    sqlc.narg(ongoing_state), sqlc.narg(source_discussion_id),
    sqlc.arg(required_confirmations), sqlc.arg(created_by_source_id),
    sqlc.arg(create_idempotency_key), sqlc.arg(create_request_hash)
)
RETURNING *;

-- name: GetAgentEventRequest :one
SELECT *
FROM events
WHERE workspace_id = sqlc.arg(workspace_id)
  AND actor_source_id = sqlc.arg(actor_source_id)
  AND event_type = sqlc.arg(event_type)
  AND idempotency_key = sqlc.arg(idempotency_key);

-- name: CreateAgentEvent :one
INSERT INTO events (
    id, workspace_id, card_id, event_type, actor_source_id,
    content, checkin_status, idempotency_key, request_hash
)
VALUES (
    sqlc.arg(id), sqlc.arg(workspace_id), sqlc.arg(card_id), sqlc.arg(event_type),
    sqlc.arg(actor_source_id), sqlc.arg(content),
    sqlc.narg(checkin_status), sqlc.arg(idempotency_key), sqlc.arg(request_hash)
)
RETURNING *;

-- name: ListRecentCardTimeline :many
SELECT *
FROM (
    SELECT
        e.*,
        s.name AS actor_name,
        s.kind AS actor_kind
    FROM events e
    JOIN sources s
      ON s.workspace_id = e.workspace_id
     AND s.id = e.actor_source_id
    WHERE e.workspace_id = sqlc.arg(workspace_id)
      AND e.card_id = sqlc.arg(card_id)
    ORDER BY e.sequence DESC
    LIMIT sqlc.arg(event_limit)
) recent
ORDER BY sequence;

-- name: CountCardConfirmations :one
SELECT count(*)
FROM events
WHERE workspace_id = sqlc.arg(workspace_id)
  AND card_id = sqlc.arg(card_id)
  AND event_type = 'confirm';
