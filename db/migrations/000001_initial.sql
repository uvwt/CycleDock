-- +goose Up

CREATE TABLE workspaces (
    id uuid PRIMARY KEY,
    name text NOT NULL CHECK (btrim(name) <> ''),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE sources (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    kind text NOT NULL CHECK (kind IN ('human', 'cycle_agent', 'oauth_client')),
    name text NOT NULL CHECK (btrim(name) <> ''),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, id)
);

CREATE TABLE projects (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    name text NOT NULL CHECK (btrim(name) <> ''),
    description text NOT NULL DEFAULT '',
    instructions text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, id)
);

CREATE TABLE cards (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    project_id uuid NOT NULL,
    kind text NOT NULL CHECK (kind IN ('normal', 'ongoing')),
    title text NOT NULL CHECK (btrim(title) <> ''),
    description text NOT NULL DEFAULT '',
    normal_column text NULL CHECK (normal_column IN ('discussion', 'todo', 'working', 'review', 'done')),
    ongoing_state text NULL CHECK (ongoing_state IN ('active', 'paused', 'archived')),
    required_confirmations integer NOT NULL DEFAULT 1 CHECK (required_confirmations > 0),
    created_by_source_id uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, id),
    CONSTRAINT cards_project_fk
        FOREIGN KEY (workspace_id, project_id)
        REFERENCES projects(workspace_id, id)
        ON DELETE CASCADE,
    CONSTRAINT cards_source_fk
        FOREIGN KEY (workspace_id, created_by_source_id)
        REFERENCES sources(workspace_id, id),
    CONSTRAINT cards_kind_state_ck CHECK (
        (kind = 'normal' AND normal_column IS NOT NULL AND ongoing_state IS NULL)
        OR
        (kind = 'ongoing' AND normal_column IS NULL AND ongoing_state IS NOT NULL)
    )
);

CREATE TABLE card_schedules (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    card_id uuid NOT NULL,
    enabled boolean NOT NULL DEFAULT true,
    frequency text NOT NULL CHECK (frequency IN ('daily', 'weekly', 'monthly')),
    day_of_week smallint NULL CHECK (day_of_week BETWEEN 0 AND 6),
    day_of_month smallint NULL CHECK (day_of_month BETWEEN 1 AND 28),
    time_of_day time NOT NULL,
    timezone text NOT NULL CHECK (btrim(timezone) <> ''),
    next_trigger_at timestamptz NOT NULL,
    last_triggered_at timestamptz NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, card_id, id),
    CONSTRAINT card_schedules_card_fk
        FOREIGN KEY (workspace_id, card_id)
        REFERENCES cards(workspace_id, id)
        ON DELETE CASCADE,
    CONSTRAINT card_schedules_shape_ck CHECK (
        (frequency = 'daily' AND day_of_week IS NULL AND day_of_month IS NULL)
        OR
        (frequency = 'weekly' AND day_of_week IS NOT NULL AND day_of_month IS NULL)
        OR
        (frequency = 'monthly' AND day_of_week IS NULL AND day_of_month IS NOT NULL)
    )
);

CREATE TABLE events (
    id uuid PRIMARY KEY,
    sequence bigint GENERATED ALWAYS AS IDENTITY UNIQUE,
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    card_id uuid NOT NULL,
    event_type text NOT NULL CHECK (event_type IN (
        'comment', 'progress', 'finding', 'result', 'checkin', 'confirm', 'reject', 'status_change'
    )),
    actor_source_id uuid NOT NULL,
    run_id uuid NULL,
    content text NOT NULL DEFAULT '',
    checkin_status text NULL CHECK (checkin_status IN ('healthy', 'warning', 'critical')),
    idempotency_key text NULL,
    request_hash text NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, card_id, id),
    CONSTRAINT events_card_fk
        FOREIGN KEY (workspace_id, card_id)
        REFERENCES cards(workspace_id, id)
        ON DELETE CASCADE,
    CONSTRAINT events_source_fk
        FOREIGN KEY (workspace_id, actor_source_id)
        REFERENCES sources(workspace_id, id),
    CONSTRAINT events_checkin_ck CHECK (
        (event_type = 'checkin' AND checkin_status IS NOT NULL)
        OR
        (event_type <> 'checkin' AND checkin_status IS NULL)
    )
);

CREATE UNIQUE INDEX events_comment_idempotency_uq
    ON events(workspace_id, actor_source_id, idempotency_key)
    WHERE event_type = 'comment' AND idempotency_key IS NOT NULL;

CREATE TABLE triggers (
    id uuid PRIMARY KEY,
    sequence bigint GENERATED ALWAYS AS IDENTITY UNIQUE,
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    card_id uuid NOT NULL,
    cause text NOT NULL CHECK (cause IN ('human_comment', 'scheduled_followup', 'manual_retry', 'system_followup')),
    cause_event_id uuid NULL,
    created_by_source_id uuid NULL,
    dispatch_mode text NOT NULL CHECK (dispatch_mode IN ('enqueue', 'interrupt')),
    schedule_id uuid NULL,
    scheduled_for timestamptz NULL,
    idempotency_key text NULL,
    request_hash text NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, card_id, id),
    CONSTRAINT triggers_card_fk
        FOREIGN KEY (workspace_id, card_id)
        REFERENCES cards(workspace_id, id)
        ON DELETE CASCADE,
    CONSTRAINT triggers_event_fk
        FOREIGN KEY (workspace_id, card_id, cause_event_id)
        REFERENCES events(workspace_id, card_id, id),
    CONSTRAINT triggers_source_fk
        FOREIGN KEY (workspace_id, created_by_source_id)
        REFERENCES sources(workspace_id, id),
    CONSTRAINT triggers_schedule_fk
        FOREIGN KEY (workspace_id, card_id, schedule_id)
        REFERENCES card_schedules(workspace_id, card_id, id)
        ON DELETE CASCADE,
    CONSTRAINT triggers_cause_shape_ck CHECK (
        (cause = 'human_comment' AND cause_event_id IS NOT NULL AND schedule_id IS NULL AND scheduled_for IS NULL)
        OR
        (cause = 'scheduled_followup' AND cause_event_id IS NULL AND schedule_id IS NOT NULL AND scheduled_for IS NOT NULL)
        OR
        (cause IN ('manual_retry', 'system_followup') AND schedule_id IS NULL AND scheduled_for IS NULL)
    )
);

CREATE UNIQUE INDEX triggers_cause_event_uq
    ON triggers(cause_event_id)
    WHERE cause_event_id IS NOT NULL;

CREATE UNIQUE INDEX triggers_schedule_occurrence_uq
    ON triggers(schedule_id, scheduled_for)
    WHERE schedule_id IS NOT NULL;

CREATE TABLE agent_runs (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    card_id uuid NOT NULL,
    trigger_id uuid NOT NULL,
    runner_source_id uuid NOT NULL,
    status text NOT NULL CHECK (status IN (
        'queued', 'running', 'waiting_human', 'completed', 'failed', 'superseded', 'expired', 'cancelled'
    )),
    current_stage text NOT NULL DEFAULT '',
    progress_summary text NOT NULL DEFAULT '',
    result_summary text NOT NULL DEFAULT '',
    error_summary text NOT NULL DEFAULT '',
    started_at timestamptz NULL,
    heartbeat_at timestamptz NULL,
    lease_expires_at timestamptz NULL,
    finished_at timestamptz NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, card_id, id),
    CONSTRAINT agent_runs_trigger_uq UNIQUE (trigger_id),
    CONSTRAINT agent_runs_card_fk
        FOREIGN KEY (workspace_id, card_id)
        REFERENCES cards(workspace_id, id)
        ON DELETE CASCADE,
    CONSTRAINT agent_runs_trigger_fk
        FOREIGN KEY (workspace_id, card_id, trigger_id)
        REFERENCES triggers(workspace_id, card_id, id),
    CONSTRAINT agent_runs_runner_fk
        FOREIGN KEY (workspace_id, runner_source_id)
        REFERENCES sources(workspace_id, id),
    CONSTRAINT agent_runs_lifecycle_ck CHECK (
        (status = 'queued' AND started_at IS NULL AND finished_at IS NULL)
        OR
        (status = 'running' AND started_at IS NOT NULL AND heartbeat_at IS NOT NULL AND lease_expires_at IS NOT NULL AND finished_at IS NULL)
        OR
        (status = 'waiting_human' AND started_at IS NOT NULL AND heartbeat_at IS NULL AND lease_expires_at IS NULL AND finished_at IS NULL)
        OR
        (status IN ('completed', 'failed', 'superseded', 'expired', 'cancelled') AND finished_at IS NOT NULL)
    )
);

CREATE UNIQUE INDEX agent_runs_one_current_per_card_uq
    ON agent_runs(workspace_id, card_id)
    WHERE status IN ('queued', 'running', 'waiting_human');

ALTER TABLE events
    ADD CONSTRAINT events_run_fk
    FOREIGN KEY (workspace_id, card_id, run_id)
    REFERENCES agent_runs(workspace_id, card_id, id);

CREATE INDEX projects_workspace_idx ON projects(workspace_id, created_at);
CREATE INDEX cards_project_idx ON cards(workspace_id, project_id, created_at);
CREATE INDEX events_card_sequence_idx ON events(workspace_id, card_id, sequence);
CREATE INDEX triggers_pending_idx ON triggers(workspace_id, card_id, sequence);
CREATE INDEX agent_runs_card_created_idx ON agent_runs(workspace_id, card_id, created_at);
CREATE INDEX card_schedules_due_idx ON card_schedules(next_trigger_at) WHERE enabled;

-- +goose Down

ALTER TABLE events DROP CONSTRAINT IF EXISTS events_run_fk;
DROP TABLE IF EXISTS agent_runs;
DROP TABLE IF EXISTS triggers;
DROP TABLE IF EXISTS events;
DROP TABLE IF EXISTS card_schedules;
DROP TABLE IF EXISTS cards;
DROP TABLE IF EXISTS projects;
DROP TABLE IF EXISTS sources;
DROP TABLE IF EXISTS workspaces;
