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
    current_run.status AS current_run_status
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
LEFT JOIN agent_runs current_run
  ON current_run.workspace_id = c.workspace_id
 AND current_run.card_id = c.id
 AND current_run.status IN ('queued', 'running', 'waiting_human')
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
    latest_run.status AS last_run_status,
    latest_run.finished_at AS last_run_finished_at
FROM card_schedules cs
JOIN cards c
  ON c.workspace_id = cs.workspace_id
 AND c.id = cs.card_id
LEFT JOIN LATERAL (
    SELECT r.status, r.finished_at
    FROM triggers t
    LEFT JOIN agent_runs r
      ON r.workspace_id = t.workspace_id
     AND r.trigger_id = t.id
    WHERE t.workspace_id = cs.workspace_id
      AND t.card_id = cs.card_id
      AND t.schedule_id = cs.id
    ORDER BY t.scheduled_for DESC, t.created_at DESC
    LIMIT 1
) latest_run ON true
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
        r.id,
        r.card_id,
        c.title AS card_title,
        c.kind AS card_kind,
        'run'::text AS activity_type,
        r.status,
        COALESCE(NULLIF(r.error_summary, ''), NULLIF(r.result_summary, ''), NULLIF(r.progress_summary, ''), NULLIF(r.current_stage, ''), t.cause) AS content,
        s.name AS actor_name,
        COALESCE(r.finished_at, r.started_at, r.created_at) AS occurred_at
    FROM agent_runs r
    JOIN cards c
      ON c.workspace_id = r.workspace_id
     AND c.id = r.card_id
    JOIN triggers t
      ON t.workspace_id = r.workspace_id
     AND t.card_id = r.card_id
     AND t.id = r.trigger_id
    JOIN sources s
      ON s.workspace_id = r.workspace_id
     AND s.id = r.runner_source_id
    WHERE r.workspace_id = sqlc.arg(workspace_id)
      AND c.project_id = sqlc.arg(project_id)

    UNION ALL

    SELECT
        t.id,
        t.card_id,
        c.title AS card_title,
        c.kind AS card_kind,
        'trigger'::text AS activity_type,
        CASE WHEN t.cancelled_at IS NULL THEN 'queued' ELSE 'cancelled' END AS status,
        COALESCE(NULLIF(t.cancel_reason, ''), t.cause) AS content,
        COALESCE(s.name, '') AS actor_name,
        COALESCE(t.cancelled_at, t.created_at) AS occurred_at
    FROM triggers t
    JOIN cards c
      ON c.workspace_id = t.workspace_id
     AND c.id = t.card_id
    LEFT JOIN sources s
      ON s.workspace_id = t.workspace_id
     AND s.id = t.created_by_source_id
    LEFT JOIN agent_runs r
      ON r.workspace_id = t.workspace_id
     AND r.trigger_id = t.id
    WHERE t.workspace_id = sqlc.arg(workspace_id)
      AND c.project_id = sqlc.arg(project_id)
      AND (r.id IS NULL OR t.cancelled_at IS NOT NULL)
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

-- name: GetCurrentRun :one
SELECT *
FROM agent_runs
WHERE workspace_id = sqlc.arg(workspace_id)
  AND card_id = sqlc.arg(card_id)
  AND status IN ('queued', 'running', 'waiting_human')
LIMIT 1;

-- name: GetTriggerLineage :one
WITH RECURSIVE lineage AS (
    SELECT t.id, t.retry_of_trigger_id, t.cause_event_id, 0 AS depth
    FROM triggers t
    WHERE t.workspace_id = sqlc.arg(workspace_id)
      AND t.card_id = sqlc.arg(card_id)
      AND t.id = sqlc.arg(trigger_id)

    UNION ALL

    SELECT parent.id, parent.retry_of_trigger_id, parent.cause_event_id, lineage.depth + 1
    FROM triggers parent
    JOIN lineage ON lineage.retry_of_trigger_id = parent.id
    WHERE parent.workspace_id = sqlc.arg(workspace_id)
      AND parent.card_id = sqlc.arg(card_id)
)
SELECT
    current.retry_of_trigger_id,
    (
        SELECT ancestor.cause_event_id
        FROM lineage ancestor
        WHERE ancestor.cause_event_id IS NOT NULL
        ORDER BY ancestor.depth
        LIMIT 1
    ) AS source_event_id
FROM lineage current
WHERE current.depth = 0;

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
    s.kind AS actor_kind,
    t.id AS trigger_id
FROM events e
JOIN sources s
  ON s.workspace_id = e.workspace_id
 AND s.id = e.actor_source_id
LEFT JOIN triggers t
  ON t.workspace_id = e.workspace_id
 AND t.card_id = e.card_id
 AND t.cause_event_id = e.id
WHERE e.workspace_id = sqlc.arg(workspace_id)
  AND e.card_id = sqlc.arg(card_id)
ORDER BY e.sequence;

-- name: ListPendingQueue :many
WITH RECURSIVE trigger_roots AS (
    SELECT root.id, root.cause_event_id AS source_event_id
    FROM triggers root
    WHERE root.workspace_id = sqlc.arg(workspace_id)
      AND root.card_id = sqlc.arg(card_id)
      AND root.retry_of_trigger_id IS NULL

    UNION ALL

    SELECT child.id, parent.source_event_id
    FROM triggers child
    JOIN trigger_roots parent ON child.retry_of_trigger_id = parent.id
    WHERE child.workspace_id = sqlc.arg(workspace_id)
      AND child.card_id = sqlc.arg(card_id)
)
SELECT
    t.id,
    t.sequence,
    t.cause,
    t.dispatch_mode,
    t.cause_event_id,
    t.retry_of_trigger_id,
    trigger_roots.source_event_id,
    COALESCE(e.content, '') AS content,
    t.created_at
FROM triggers t
JOIN trigger_roots ON trigger_roots.id = t.id
LEFT JOIN events e
  ON e.workspace_id = t.workspace_id
 AND e.card_id = t.card_id
 AND e.id = t.cause_event_id
LEFT JOIN agent_runs r
  ON r.workspace_id = t.workspace_id
 AND r.trigger_id = t.id
WHERE t.workspace_id = sqlc.arg(workspace_id)
  AND t.card_id = sqlc.arg(card_id)
  AND t.cancelled_at IS NULL
  AND r.id IS NULL
ORDER BY t.sequence;

-- name: ListRecentCardWork :many
WITH RECURSIVE trigger_roots AS (
    SELECT id, cause_event_id AS source_event_id
    FROM triggers
    WHERE workspace_id = sqlc.arg(workspace_id)
      AND card_id = sqlc.arg(card_id)
      AND retry_of_trigger_id IS NULL

    UNION ALL

    SELECT child.id, parent.source_event_id
    FROM triggers child
    JOIN trigger_roots parent ON child.retry_of_trigger_id = parent.id
    WHERE child.workspace_id = sqlc.arg(workspace_id)
      AND child.card_id = sqlc.arg(card_id)
)
SELECT
    t.id AS trigger_id,
    t.sequence,
    t.cause,
    t.dispatch_mode,
    t.cause_event_id,
    trigger_roots.source_event_id,
    COALESCE(e.content, '') AS content,
    t.retry_of_trigger_id,
    t.cancelled_at,
    t.cancel_reason,
    t.created_at AS trigger_created_at,
    r.id AS run_id,
    r.status AS run_status,
    r.current_stage,
    r.progress_summary,
    r.result_summary,
    r.error_summary,
    r.cancel_reason AS run_cancel_reason,
    r.created_at AS run_created_at,
    r.finished_at,
    EXISTS (
        SELECT 1
        FROM triggers child
        WHERE child.workspace_id = t.workspace_id
          AND child.card_id = t.card_id
          AND child.retry_of_trigger_id = t.id
    ) AS has_retry
FROM triggers t
JOIN trigger_roots ON trigger_roots.id = t.id
LEFT JOIN events e
  ON e.workspace_id = t.workspace_id
 AND e.card_id = t.card_id
 AND e.id = t.cause_event_id
LEFT JOIN agent_runs r
  ON r.workspace_id = t.workspace_id
 AND r.trigger_id = t.id
WHERE t.workspace_id = sqlc.arg(workspace_id)
  AND t.card_id = sqlc.arg(card_id)
  AND (
      t.cancelled_at IS NOT NULL
      OR r.status IN ('completed', 'failed', 'superseded', 'expired', 'cancelled')
  )
ORDER BY t.sequence DESC
LIMIT 20;

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

-- name: ScheduleHasTriggers :one
SELECT EXISTS (
    SELECT 1
    FROM triggers
    WHERE workspace_id = sqlc.arg(workspace_id)
      AND card_id = sqlc.arg(card_id)
      AND schedule_id = sqlc.arg(schedule_id)::uuid
);
