-- name: ListProjects :many
SELECT *
FROM projects
WHERE workspace_id = sqlc.arg(workspace_id)
ORDER BY created_at, id;

-- name: GetProject :one
SELECT *
FROM projects
WHERE workspace_id = sqlc.arg(workspace_id)
  AND id = sqlc.arg(id);

-- name: UpdateProject :one
UPDATE projects
SET name = sqlc.arg(name),
    description = sqlc.arg(description),
    instructions = sqlc.arg(instructions),
    updated_at = now()
WHERE workspace_id = sqlc.arg(workspace_id)
  AND id = sqlc.arg(id)
RETURNING *;

-- name: ListProjectCards :many
SELECT *
FROM cards
WHERE workspace_id = sqlc.arg(workspace_id)
  AND project_id = sqlc.arg(project_id)
ORDER BY created_at, id;

-- name: ListProjectCardSummaries :many
SELECT
    c.*,
    COALESCE(latest_checkin.checkin_status, 'unknown') AS health,
    current_attempt.status AS current_attempt_status
FROM cards c
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
  AND c.project_id = sqlc.arg(project_id)
ORDER BY c.created_at, c.id;

-- name: ListProjectSchedules :many
SELECT
    cs.id,
    cs.card_id,
    c.title AS card_title,
    c.ongoing_state,
    cs.enabled,
    cs.frequency,
    cs.day_of_week,
    cs.day_of_month,
    cs.time_of_day,
    cs.timezone,
    cs.next_trigger_at,
    cs.last_triggered_at,
    latest_attempt.status AS last_attempt_status,
    latest_attempt.finished_at AS last_attempt_finished_at
FROM card_schedules cs
JOIN cards c
  ON c.workspace_id = cs.workspace_id
 AND c.id = cs.card_id
LEFT JOIN LATERAL (
    SELECT a.status, a.finished_at
    FROM card_turns t
    LEFT JOIN turn_attempts a
      ON a.workspace_id = t.workspace_id
     AND a.turn_id = t.id
    WHERE t.workspace_id = cs.workspace_id
      AND t.card_id = cs.card_id
      AND t.schedule_id = cs.id
    ORDER BY t.scheduled_for DESC, a.attempt_number DESC NULLS LAST, t.created_at DESC
    LIMIT 1
) latest_attempt ON true
WHERE cs.workspace_id = sqlc.arg(workspace_id)
  AND c.project_id = sqlc.arg(project_id)
  AND c.kind = 'ongoing'
ORDER BY cs.enabled DESC, cs.next_trigger_at, cs.id;

-- name: ListProjectActivity :many
WITH activity AS (
    SELECT
        e.id,
        e.card_id,
        c.title AS card_title,
        c.kind AS card_kind,
        'event'::text AS activity_type,
        e.event_type AS status,
        e.content,
        s.name AS actor_name,
        e.created_at AS occurred_at
    FROM events e
    JOIN cards c
      ON c.workspace_id = e.workspace_id
     AND c.id = e.card_id
    JOIN sources s
      ON s.workspace_id = e.workspace_id
     AND s.id = e.actor_source_id
    WHERE e.workspace_id = sqlc.arg(workspace_id)
      AND c.project_id = sqlc.arg(project_id)
      AND e.event_type IN ('progress', 'finding', 'result', 'checkin', 'confirm', 'reject', 'status_change')

    UNION ALL

    SELECT
        a.id,
        a.card_id,
        c.title AS card_title,
        c.kind AS card_kind,
        'attempt'::text AS activity_type,
        a.status,
        COALESCE(NULLIF(a.error_summary, ''), NULLIF(a.result_summary, ''), NULLIF(a.progress_summary, ''), NULLIF(a.current_stage, ''), t.content) AS content,
        s.name AS actor_name,
        COALESCE(a.finished_at, a.started_at, a.created_at) AS occurred_at
    FROM turn_attempts a
    JOIN cards c
      ON c.workspace_id = a.workspace_id
     AND c.id = a.card_id
    JOIN card_turns t
      ON t.workspace_id = a.workspace_id
     AND t.card_id = a.card_id
     AND t.id = a.turn_id
    JOIN sources s
      ON s.workspace_id = a.workspace_id
     AND s.id = a.runner_source_id
    WHERE a.workspace_id = sqlc.arg(workspace_id)
      AND c.project_id = sqlc.arg(project_id)

    UNION ALL

    SELECT
        t.id,
        t.card_id,
        c.title AS card_title,
        c.kind AS card_kind,
        'turn'::text AS activity_type,
        CASE WHEN t.cancelled_at IS NULL THEN 'queued' ELSE 'cancelled' END AS status,
        COALESCE(NULLIF(t.cancel_reason, ''), t.content) AS content,
        COALESCE(s.name, '') AS actor_name,
        COALESCE(t.cancelled_at, t.created_at) AS occurred_at
    FROM card_turns t
    JOIN cards c
      ON c.workspace_id = t.workspace_id
     AND c.id = t.card_id
    LEFT JOIN sources s
      ON s.workspace_id = t.workspace_id
     AND s.id = t.created_by_source_id
    LEFT JOIN turn_attempts a
      ON a.workspace_id = t.workspace_id
     AND a.turn_id = t.id
    WHERE t.workspace_id = sqlc.arg(workspace_id)
      AND c.project_id = sqlc.arg(project_id)
      AND (a.id IS NULL OR t.cancelled_at IS NOT NULL)
)
SELECT *
FROM activity
ORDER BY occurred_at DESC, id DESC
LIMIT sqlc.arg(activity_limit)::int;

-- name: UpdateCardText :one
UPDATE cards
SET title = sqlc.arg(title),
    description = sqlc.arg(description),
    updated_at = now()
WHERE workspace_id = sqlc.arg(workspace_id)
  AND id = sqlc.arg(id)
RETURNING *;

-- name: ListCardSchedules :many
SELECT *
FROM card_schedules
WHERE workspace_id = sqlc.arg(workspace_id)
  AND card_id = sqlc.arg(card_id)
ORDER BY created_at, id;

-- name: GetCardSchedule :one
SELECT *
FROM card_schedules
WHERE workspace_id = sqlc.arg(workspace_id)
  AND card_id = sqlc.arg(card_id)
  AND id = sqlc.arg(id);

-- name: LatestCheckinForCard :one
SELECT *
FROM events
WHERE workspace_id = sqlc.arg(workspace_id)
  AND card_id = sqlc.arg(card_id)
  AND event_type = 'checkin'
ORDER BY sequence DESC
LIMIT 1;

-- name: ListCardTimeline :many
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
ORDER BY e.sequence;

-- name: UpdateCardSchedule :execrows
UPDATE card_schedules
SET enabled = sqlc.arg(enabled),
    frequency = sqlc.arg(frequency),
    day_of_week = sqlc.narg(day_of_week),
    day_of_month = sqlc.narg(day_of_month),
    time_of_day = sqlc.arg(time_of_day),
    timezone = sqlc.arg(timezone),
    next_trigger_at = sqlc.arg(next_trigger_at),
    updated_at = now()
WHERE workspace_id = sqlc.arg(workspace_id)
  AND card_id = sqlc.arg(card_id)
  AND id = sqlc.arg(id);

-- name: DeleteCardSchedule :execrows
DELETE FROM card_schedules
WHERE workspace_id = sqlc.arg(workspace_id)
  AND card_id = sqlc.arg(card_id)
  AND id = sqlc.arg(id);

-- name: ScheduleHasTurns :one
SELECT EXISTS (
    SELECT 1
    FROM card_turns
    WHERE workspace_id = sqlc.arg(workspace_id)
      AND card_id = sqlc.arg(card_id)
      AND schedule_id = sqlc.arg(schedule_id)::uuid
);
