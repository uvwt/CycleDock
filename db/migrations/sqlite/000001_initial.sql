-- +goose NO TRANSACTION
-- +goose Up
-- CycleDock SQLite 初始完整 Schema 基线（与 PostgreSQL 000001..000051 最终 Schema 严格等价）。
-- 所有时间戳统一采用定长 30 字节 UTC RFC3339Nano（YYYY-MM-DDTHH:MM:SS.fffffffffZ），保证文本字典序与时间序严格一致。
-- 所有 JSON 列存为 UTF-8 TEXT，字符串/UUID 数组列存为 JSON 数组 TEXT，密文/摘要列存为 BLOB。

CREATE TABLE agent_app_state (
    app_name TEXT NOT NULL,
    state TEXT NOT NULL DEFAULT '{}',
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000000Z', 'now')),
    CONSTRAINT agent_app_state_pkey PRIMARY KEY (app_name),
    CONSTRAINT agent_app_state_app_name_check CHECK (trim(app_name) <> ''),
    CONSTRAINT agent_app_state_state_check CHECK ((json_valid(state) AND json_type(state) = 'object'))
);

CREATE TABLE agent_artifacts (
    app_name TEXT NOT NULL,
    user_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    file_name TEXT NOT NULL,
    version INTEGER NOT NULL,
    part TEXT NOT NULL,
    mime_type TEXT NOT NULL DEFAULT '',
    custom_metadata TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000000Z', 'now')),
    CONSTRAINT agent_artifacts_pkey PRIMARY KEY (app_name, user_id, session_id, file_name, version),
    CONSTRAINT agent_artifacts_app_name_check CHECK (trim(app_name) <> ''),
    CONSTRAINT agent_artifacts_custom_metadata_check CHECK ((json_valid(custom_metadata) AND json_type(custom_metadata) = 'object')),
    CONSTRAINT agent_artifacts_file_name_check CHECK (trim(file_name) <> ''),
    CONSTRAINT agent_artifacts_part_check CHECK ((json_valid(part) AND json_type(part) = 'object')),
    CONSTRAINT agent_artifacts_session_id_check CHECK (trim(session_id) <> ''),
    CONSTRAINT agent_artifacts_user_id_check CHECK (trim(user_id) <> ''),
    CONSTRAINT agent_artifacts_version_check CHECK (version > 0),
    CONSTRAINT agent_artifacts_session_fk FOREIGN KEY (app_name, user_id, session_id) REFERENCES agent_sessions(app_name, user_id, id) ON DELETE CASCADE
);

CREATE TABLE agent_model_calls (
    id TEXT NOT NULL,
    workspace_id TEXT NOT NULL,
    card_id TEXT NOT NULL,
    turn_id TEXT NOT NULL,
    attempt_id TEXT NOT NULL,
    invocation_id TEXT NOT NULL DEFAULT '',
    call_index INTEGER NOT NULL,
    purpose TEXT NOT NULL,
    model_route_id TEXT,
    model_id TEXT NOT NULL,
    provider TEXT NOT NULL,
    started_at TEXT NOT NULL,
    finished_at TEXT NOT NULL,
    duration_ms INTEGER NOT NULL,
    status TEXT NOT NULL,
    input_tokens INTEGER NOT NULL DEFAULT 0,
    output_tokens INTEGER NOT NULL DEFAULT 0,
    cached_input_tokens INTEGER NOT NULL DEFAULT 0,
    reasoning_output_tokens INTEGER NOT NULL DEFAULT 0,
    usage_source TEXT NOT NULL DEFAULT 'unknown',
    error_code TEXT NOT NULL DEFAULT '',
    error_summary TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000000Z', 'now')),
    response_model TEXT NOT NULL DEFAULT '',
    CONSTRAINT agent_model_calls_pkey PRIMARY KEY (workspace_id, card_id, id),
    CONSTRAINT agent_model_calls_duration_check CHECK (duration_ms >= 0),
    CONSTRAINT agent_model_calls_purpose_check CHECK (purpose IN ('agent', 'compaction')),
    CONSTRAINT agent_model_calls_status_check CHECK (status IN ('success', 'failed')),
    CONSTRAINT agent_model_calls_usage_source_check CHECK (usage_source IN ('actual', 'estimated', 'unknown')),
    CONSTRAINT agent_model_calls_attempt_fk FOREIGN KEY (workspace_id, card_id, attempt_id) REFERENCES turn_attempts(workspace_id, card_id, id) ON DELETE CASCADE,
    CONSTRAINT agent_model_calls_card_fk FOREIGN KEY (workspace_id, card_id) REFERENCES cards(workspace_id, id) ON DELETE CASCADE
);

CREATE TABLE agent_session_events (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    app_name TEXT NOT NULL,
    user_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    id TEXT NOT NULL,
    invocation_id TEXT NOT NULL DEFAULT '',
    occurred_at TEXT NOT NULL,
    event TEXT NOT NULL,
    CONSTRAINT agent_session_events_event_check CHECK ((json_valid(event) AND json_type(event) = 'object')),
    CONSTRAINT agent_session_events_id_check CHECK (trim(id) <> ''),
    CONSTRAINT agent_session_events_session_fk FOREIGN KEY (app_name, user_id, session_id) REFERENCES agent_sessions(app_name, user_id, id) ON DELETE CASCADE
);

CREATE TABLE agent_sessions (
    app_name TEXT NOT NULL,
    user_id TEXT NOT NULL,
    id TEXT NOT NULL,
    state TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000000Z', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000000Z', 'now')),
    CONSTRAINT agent_sessions_pkey PRIMARY KEY (app_name, user_id, id),
    CONSTRAINT agent_sessions_app_name_check CHECK (trim(app_name) <> ''),
    CONSTRAINT agent_sessions_id_check CHECK (trim(id) <> ''),
    CONSTRAINT agent_sessions_state_check CHECK ((json_valid(state) AND json_type(state) = 'object')),
    CONSTRAINT agent_sessions_user_id_check CHECK (trim(user_id) <> '')
);

CREATE TABLE agent_user_state (
    app_name TEXT NOT NULL,
    user_id TEXT NOT NULL,
    state TEXT NOT NULL DEFAULT '{}',
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000000Z', 'now')),
    CONSTRAINT agent_user_state_pkey PRIMARY KEY (app_name, user_id),
    CONSTRAINT agent_user_state_app_name_check CHECK (trim(app_name) <> ''),
    CONSTRAINT agent_user_state_state_check CHECK ((json_valid(state) AND json_type(state) = 'object')),
    CONSTRAINT agent_user_state_user_id_check CHECK (trim(user_id) <> '')
);

CREATE TABLE card_attachments (
    workspace_id TEXT NOT NULL,
    card_id TEXT NOT NULL,
    attachment_id TEXT NOT NULL,
    position INTEGER NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000000Z', 'now')),
    CONSTRAINT card_attachments_pkey PRIMARY KEY (workspace_id, card_id, attachment_id),
    CONSTRAINT card_attachments_position_check CHECK ("position" >= 0),
    CONSTRAINT card_attachments_attachment_fk FOREIGN KEY (attachment_id) REFERENCES workspace_attachments(id) ON DELETE CASCADE,
    CONSTRAINT card_attachments_card_fk FOREIGN KEY (workspace_id, card_id) REFERENCES cards(workspace_id, id) ON DELETE CASCADE
);

CREATE TABLE card_read_cursors (
    workspace_id TEXT NOT NULL,
    source_id TEXT NOT NULL,
    card_id TEXT NOT NULL,
    last_read_message_sequence INTEGER NOT NULL DEFAULT 0,
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000000Z', 'now')),
    marked_unread INTEGER NOT NULL DEFAULT 0,
    CONSTRAINT card_read_cursors_pkey PRIMARY KEY (workspace_id, source_id, card_id),
    CONSTRAINT card_read_cursors_sequence_check CHECK (last_read_message_sequence >= 0),
    CONSTRAINT card_read_cursors_card_fkey FOREIGN KEY (workspace_id, card_id) REFERENCES cards(workspace_id, id) ON DELETE CASCADE,
    CONSTRAINT card_read_cursors_source_fkey FOREIGN KEY (workspace_id, source_id) REFERENCES sources(workspace_id, id) ON DELETE CASCADE
);

CREATE TABLE card_schedules (
    id TEXT NOT NULL,
    workspace_id TEXT NOT NULL,
    card_id TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1,
    frequency TEXT NOT NULL,
    day_of_week INTEGER,
    day_of_month INTEGER,
    time_of_day TEXT,
    timezone TEXT NOT NULL,
    next_trigger_at TEXT NOT NULL,
    last_triggered_at TEXT,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000000Z', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000000Z', 'now')),
    interval_minutes INTEGER,
    archived_at TEXT,
    CONSTRAINT card_schedules_pkey PRIMARY KEY (id),
    CONSTRAINT card_schedules_workspace_id_card_id_id_key UNIQUE (workspace_id, card_id, id),
    CONSTRAINT card_schedules_workspace_id_id_key UNIQUE (workspace_id, id),
    CONSTRAINT card_schedules_archived_disabled_ck CHECK (archived_at IS NULL OR NOT enabled),
    CONSTRAINT card_schedules_day_of_month_check CHECK (day_of_month >= 1 AND day_of_month <= 28),
    CONSTRAINT card_schedules_day_of_week_check CHECK (day_of_week >= 0 AND day_of_week <= 6),
    CONSTRAINT card_schedules_frequency_check CHECK (frequency IN ('daily', 'weekly', 'monthly', 'interval')),
    CONSTRAINT card_schedules_shape_ck CHECK (frequency = 'daily' AND time_of_day IS NOT NULL AND day_of_week IS NULL AND day_of_month IS NULL AND interval_minutes IS NULL OR frequency = 'weekly' AND time_of_day IS NOT NULL AND day_of_week IS NOT NULL AND day_of_month IS NULL AND interval_minutes IS NULL OR frequency = 'monthly' AND time_of_day IS NOT NULL AND day_of_week IS NULL AND day_of_month IS NOT NULL AND interval_minutes IS NULL OR frequency = 'interval' AND time_of_day IS NULL AND day_of_week IS NULL AND day_of_month IS NULL AND interval_minutes IS NOT NULL AND interval_minutes >= 1 AND interval_minutes <= 1440),
    CONSTRAINT card_schedules_timezone_check CHECK (trim(timezone) <> ''),
    CONSTRAINT card_schedules_card_fk FOREIGN KEY (workspace_id, card_id) REFERENCES cards(workspace_id, id) ON DELETE CASCADE,
    CONSTRAINT card_schedules_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE
);

CREATE TABLE card_tool_approvals (
    id TEXT NOT NULL,
    workspace_id TEXT NOT NULL,
    card_id TEXT NOT NULL,
    turn_id TEXT,
    attempt_id TEXT,
    tool_call_id TEXT NOT NULL DEFAULT '',
    tool_name TEXT NOT NULL,
    arguments TEXT NOT NULL DEFAULT '{}',
    risk_level TEXT NOT NULL,
    reversible INTEGER NOT NULL DEFAULT 0,
    reason TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending',
    resolution_comment TEXT NOT NULL DEFAULT '',
    resolved_by_source_id TEXT,
    resolved_at TEXT,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000000Z', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000000Z', 'now')),
    scope_type TEXT NOT NULL DEFAULT '',
    scope_target TEXT NOT NULL DEFAULT '',
    scope_label TEXT NOT NULL DEFAULT '',
    allow_session_whitelist INTEGER NOT NULL DEFAULT 1,
    CONSTRAINT card_tool_approvals_pkey PRIMARY KEY (id),
    CONSTRAINT card_tool_approvals_risk_ck CHECK (risk_level IN ('low', 'medium', 'high', 'critical')),
    CONSTRAINT card_tool_approvals_scope_type_ck CHECK (scope_type IN ('', 'device', 'mcp', 'tool')),
    CONSTRAINT card_tool_approvals_status_ck CHECK (status IN ('pending', 'approved', 'rejected', 'superseded')),
    CONSTRAINT card_tool_approvals_card_fk FOREIGN KEY (workspace_id, card_id) REFERENCES cards(workspace_id, id) ON DELETE CASCADE,
    CONSTRAINT card_tool_approvals_source_fk FOREIGN KEY (workspace_id, resolved_by_source_id) REFERENCES sources(workspace_id, id) ON DELETE NO ACTION,
    CONSTRAINT card_tool_approvals_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE
);

CREATE TABLE card_tool_whitelists (
    id TEXT NOT NULL,
    workspace_id TEXT NOT NULL,
    card_id TEXT NOT NULL,
    tool_name TEXT NOT NULL DEFAULT '',
    pattern TEXT NOT NULL DEFAULT '*',
    created_by_source_id TEXT,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000000Z', 'now')),
    scope_type TEXT NOT NULL DEFAULT 'tool',
    scope_target TEXT NOT NULL DEFAULT '',
    CONSTRAINT card_tool_whitelists_pkey PRIMARY KEY (id),
    CONSTRAINT card_tool_whitelists_scope_type_ck CHECK (scope_type IN ('device', 'mcp', 'tool')),
    CONSTRAINT card_tool_whitelists_card_fk FOREIGN KEY (workspace_id, card_id) REFERENCES cards(workspace_id, id) ON DELETE CASCADE,
    CONSTRAINT card_tool_whitelists_source_fk FOREIGN KEY (workspace_id, created_by_source_id) REFERENCES sources(workspace_id, id) ON DELETE NO ACTION
);

CREATE TABLE card_turns (
    id TEXT NOT NULL,
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    workspace_id TEXT NOT NULL,
    card_id TEXT NOT NULL,
    source TEXT NOT NULL,
    content TEXT NOT NULL,
    created_by_source_id TEXT,
    source_event_id TEXT,
    dispatch_mode TEXT NOT NULL,
    schedule_id TEXT,
    scheduled_for TEXT,
    idempotency_key TEXT,
    request_hash TEXT,
    cancelled_at TEXT,
    cancelled_by_source_id TEXT,
    cancel_reason TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000000Z', 'now')),
    device_node_id TEXT NOT NULL DEFAULT '',
    device_name TEXT NOT NULL DEFAULT '',
    preferred_model_route_id TEXT,
    CONSTRAINT card_turns_pkey UNIQUE (id),
    CONSTRAINT card_turns_sequence_key UNIQUE (sequence),
    CONSTRAINT card_turns_workspace_id_card_id_id_key UNIQUE (workspace_id, card_id, id),
    CONSTRAINT card_turns_workspace_id_id_key UNIQUE (workspace_id, id),
    CONSTRAINT card_turns_cancel_shape_ck CHECK (cancelled_at IS NULL AND cancelled_by_source_id IS NULL AND cancel_reason = '' OR cancelled_at IS NOT NULL AND cancelled_by_source_id IS NOT NULL),
    CONSTRAINT card_turns_content_check CHECK (trim(content) <> ''),
    CONSTRAINT card_turns_device_target_ck CHECK (length(device_node_id) <= 200 AND length(device_name) <= 200 AND (device_node_id <> '' OR device_name = '')),
    CONSTRAINT card_turns_dispatch_mode_check CHECK (dispatch_mode IN ('enqueue', 'interrupt')),
    CONSTRAINT card_turns_source_check CHECK (source IN ('human', 'schedule', 'system', 'trigger')),
    CONSTRAINT card_turns_source_shape_ck CHECK (source = 'human' AND created_by_source_id IS NOT NULL AND schedule_id IS NULL AND scheduled_for IS NULL OR source = 'schedule' AND created_by_source_id IS NULL AND schedule_id IS NOT NULL AND scheduled_for IS NOT NULL AND dispatch_mode = 'enqueue' OR source = 'system' AND created_by_source_id IS NOT NULL AND schedule_id IS NULL AND scheduled_for IS NULL AND (dispatch_mode IN ('enqueue', 'interrupt')) OR source = 'trigger' AND created_by_source_id IS NULL AND schedule_id IS NULL AND scheduled_for IS NULL AND (dispatch_mode IN ('enqueue', 'interrupt'))),
    CONSTRAINT card_turns_cancelled_by_fk FOREIGN KEY (workspace_id, cancelled_by_source_id) REFERENCES sources(workspace_id, id),
    CONSTRAINT card_turns_card_fk FOREIGN KEY (workspace_id, card_id) REFERENCES cards(workspace_id, id) ON DELETE CASCADE,
    CONSTRAINT card_turns_created_by_fk FOREIGN KEY (workspace_id, created_by_source_id) REFERENCES sources(workspace_id, id),
    CONSTRAINT card_turns_preferred_model_route_id_fkey FOREIGN KEY (preferred_model_route_id) REFERENCES workspace_model_routes(id) ON DELETE SET NULL,
    CONSTRAINT card_turns_schedule_fk FOREIGN KEY (workspace_id, card_id, schedule_id) REFERENCES card_schedules(workspace_id, card_id, id) ON DELETE RESTRICT,
    CONSTRAINT card_turns_source_event_fk FOREIGN KEY (workspace_id, card_id, source_event_id) REFERENCES events(workspace_id, card_id, id) ON DELETE NO ACTION,
    CONSTRAINT card_turns_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE
);

CREATE TABLE card_watchers (
    id TEXT NOT NULL,
    workspace_id TEXT NOT NULL,
    card_id TEXT NOT NULL,
    kind TEXT NOT NULL,
    match_key TEXT NOT NULL DEFAULT '',
    note TEXT NOT NULL DEFAULT '',
    dispatch_mode TEXT NOT NULL DEFAULT 'enqueue',
    status TEXT NOT NULL DEFAULT 'armed',
    expires_at TEXT NOT NULL,
    created_by_source_id TEXT,
    created_by_attempt_id TEXT,
    triggered_turn_id TEXT,
    resolved_reason TEXT NOT NULL DEFAULT '',
    resolved_at TEXT,
    idempotency_key TEXT,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000000Z', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000000Z', 'now')),
    CONSTRAINT card_watchers_pkey PRIMARY KEY (id),
    CONSTRAINT card_watchers_workspace_id_id_key UNIQUE (workspace_id, id),
    CONSTRAINT card_watchers_dispatch_mode_check CHECK (dispatch_mode IN ('enqueue', 'interrupt')),
    CONSTRAINT card_watchers_kind_check CHECK (kind IN ('event', 'wakeup')),
    CONSTRAINT card_watchers_kind_shape_ck CHECK (kind = 'event' AND trim(match_key) <> '' OR kind = 'wakeup' AND match_key = ''),
    CONSTRAINT card_watchers_status_check CHECK (status IN ('armed', 'triggered', 'expired', 'cancelled')),
    CONSTRAINT card_watchers_attempt_fk FOREIGN KEY (workspace_id, card_id, created_by_attempt_id) REFERENCES turn_attempts(workspace_id, card_id, id) ON DELETE NO ACTION,
    CONSTRAINT card_watchers_card_fk FOREIGN KEY (workspace_id, card_id) REFERENCES cards(workspace_id, id) ON DELETE CASCADE,
    CONSTRAINT card_watchers_source_fk FOREIGN KEY (workspace_id, created_by_source_id) REFERENCES sources(workspace_id, id) ON DELETE NO ACTION,
    CONSTRAINT card_watchers_turn_fk FOREIGN KEY (workspace_id, card_id, triggered_turn_id) REFERENCES card_turns(workspace_id, card_id, id) ON DELETE NO ACTION,
    CONSTRAINT card_watchers_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE
);

CREATE TABLE cards (
    id TEXT NOT NULL,
    workspace_id TEXT NOT NULL,
    project_id TEXT NOT NULL,
    kind TEXT NOT NULL,
    title TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    discussion_state TEXT,
    todo_state TEXT,
    ongoing_state TEXT,
    source_discussion_id TEXT,
    required_confirmations INTEGER NOT NULL DEFAULT 1,
    created_by_source_id TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000000Z', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000000Z', 'now')),
    create_idempotency_key TEXT,
    create_request_hash TEXT,
    pinned_at TEXT,
    confirmation_round INTEGER NOT NULL DEFAULT 0,
    preferred_model_route_id TEXT,
    notifications_muted INTEGER NOT NULL DEFAULT 0,
    CONSTRAINT cards_pkey PRIMARY KEY (id),
    CONSTRAINT cards_workspace_id_id_key UNIQUE (workspace_id, id),
    CONSTRAINT cards_confirmation_round_check CHECK (confirmation_round >= 0),
    CONSTRAINT cards_create_request_identity_ck CHECK (create_idempotency_key IS NULL AND create_request_hash IS NULL OR create_idempotency_key IS NOT NULL AND trim(create_idempotency_key) <> '' AND create_request_hash IS NOT NULL),
    CONSTRAINT cards_discussion_state_check CHECK (discussion_state IN ('active', 'archived')),
    CONSTRAINT cards_kind_check CHECK (kind IN ('discussion', 'todo', 'ongoing')),
    CONSTRAINT cards_kind_state_ck CHECK (kind = 'discussion' AND discussion_state IS NOT NULL AND todo_state IS NULL AND ongoing_state IS NULL AND source_discussion_id IS NULL OR kind = 'todo' AND discussion_state IS NULL AND todo_state IS NOT NULL AND ongoing_state IS NULL OR kind = 'ongoing' AND discussion_state IS NULL AND todo_state IS NULL AND ongoing_state IS NOT NULL),
    CONSTRAINT cards_ongoing_state_check CHECK (ongoing_state IN ('active', 'paused', 'archived')),
    CONSTRAINT cards_required_confirmations_check CHECK (required_confirmations > 0),
    CONSTRAINT cards_title_ck CHECK (kind = 'discussion' OR trim(title) <> ''),
    CONSTRAINT cards_todo_state_check CHECK (todo_state IN ('todo', 'working', 'review', 'done')),
    CONSTRAINT cards_preferred_model_route_id_fkey FOREIGN KEY (preferred_model_route_id) REFERENCES workspace_model_routes(id) ON DELETE SET NULL,
    CONSTRAINT cards_project_fk FOREIGN KEY (workspace_id, project_id) REFERENCES projects(workspace_id, id) ON DELETE CASCADE,
    CONSTRAINT cards_source_discussion_fk FOREIGN KEY (workspace_id, source_discussion_id) REFERENCES cards(workspace_id, id) ON DELETE NO ACTION,
    CONSTRAINT cards_source_fk FOREIGN KEY (workspace_id, created_by_source_id) REFERENCES sources(workspace_id, id),
    CONSTRAINT cards_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE
);

CREATE TABLE events (
    id TEXT NOT NULL,
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    workspace_id TEXT NOT NULL,
    card_id TEXT NOT NULL,
    event_type TEXT NOT NULL,
    actor_source_id TEXT NOT NULL,
    attempt_id TEXT,
    content TEXT NOT NULL DEFAULT '',
    checkin_status TEXT,
    idempotency_key TEXT,
    request_hash TEXT,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000000Z', 'now')),
    message TEXT,
    confirmation_round INTEGER NOT NULL DEFAULT 0,
    source_card_id TEXT,
    tool_approval_id TEXT,
    CONSTRAINT events_pkey UNIQUE (id),
    CONSTRAINT events_sequence_key UNIQUE (sequence),
    CONSTRAINT events_workspace_id_card_id_id_key UNIQUE (workspace_id, card_id, id),
    CONSTRAINT events_workspace_id_id_key UNIQUE (workspace_id, id),
    CONSTRAINT events_checkin_ck CHECK (event_type = 'checkin' AND checkin_status IS NOT NULL OR event_type <> 'checkin' AND checkin_status IS NULL),
    CONSTRAINT events_checkin_status_check CHECK (checkin_status IN ('healthy', 'warning', 'critical')),
    CONSTRAINT events_confirmation_round_check CHECK (confirmation_round >= 0),
    CONSTRAINT events_event_type_check CHECK (event_type IN ('comment', 'progress', 'finding', 'result', 'checkin', 'confirm', 'reject', 'status_change', 'message', 'checkin_ack', 'tool_approval')),
    CONSTRAINT events_message_payload_check CHECK (event_type = 'message' AND message IS NOT NULL AND (json_valid(message) AND json_type(message) = 'object') OR event_type = 'comment' AND (message IS NULL OR (json_valid(message) AND json_type(message) = 'object')) OR (event_type NOT IN ('message', 'comment')) AND message IS NULL),
    CONSTRAINT events_reply_preview_check CHECK (message IS NULL OR json_type(message, '$.preview_id') IS NULL OR event_type = 'message' AND attempt_id IS NOT NULL AND json_type(message, '$.preview_id') = 'text' AND length(json_extract(message, '$.preview_id')) >= 1 AND length(json_extract(message, '$.preview_id')) <= 512 AND (json_extract(message, '$.preview') IS NOT 1 AND json_extract(message, '$.preview') IS NOT 'true' OR idempotency_key IS NULL)),
    CONSTRAINT events_attempt_fk FOREIGN KEY (workspace_id, card_id, attempt_id) REFERENCES turn_attempts(workspace_id, card_id, id) ON DELETE NO ACTION,
    CONSTRAINT events_card_fk FOREIGN KEY (workspace_id, card_id) REFERENCES cards(workspace_id, id) ON DELETE CASCADE,
    CONSTRAINT events_source_card_fk FOREIGN KEY (workspace_id, source_card_id) REFERENCES cards(workspace_id, id) ON DELETE NO ACTION,
    CONSTRAINT events_source_fk FOREIGN KEY (workspace_id, actor_source_id) REFERENCES sources(workspace_id, id),
    CONSTRAINT events_tool_approval_id_fkey FOREIGN KEY (tool_approval_id) REFERENCES card_tool_approvals(id) ON DELETE SET NULL,
    CONSTRAINT events_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE
);

CREATE TABLE human_accounts (
    id TEXT NOT NULL,
    display_name TEXT NOT NULL,
    email_snapshot TEXT,
    is_local_admin INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000000Z', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000000Z', 'now')),
    CONSTRAINT human_accounts_pkey PRIMARY KEY (id),
    CONSTRAINT human_accounts_display_name_check CHECK (trim(display_name) <> '')
);

CREATE TABLE model_auth_flows (
    id TEXT NOT NULL,
    workspace_id TEXT NOT NULL,
    provider TEXT NOT NULL,
    state_digest BLOB,
    payload_ciphertext BLOB NOT NULL,
    expires_at TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000000Z', 'now')),
    CONSTRAINT model_auth_flows_pkey PRIMARY KEY (id),
    CONSTRAINT model_auth_flows_payload_ciphertext_check CHECK (length(payload_ciphertext) > 28),
    CONSTRAINT model_auth_flows_provider_check CHECK (provider IN ('chatgpt', 'grok')),
    CONSTRAINT model_auth_flows_state_digest_check CHECK (state_digest IS NULL OR length(state_digest) = 32),
    CONSTRAINT model_auth_flows_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE
);

CREATE TABLE model_oauth_credentials (
    workspace_id TEXT NOT NULL,
    provider TEXT NOT NULL,
    credential_ciphertext BLOB NOT NULL,
    expires_at TEXT NOT NULL,
    display_label TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000000Z', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000000Z', 'now')),
    id TEXT NOT NULL,
    account_subject TEXT NOT NULL DEFAULT '',
    CONSTRAINT model_oauth_credentials_pkey PRIMARY KEY (id),
    CONSTRAINT model_oauth_credentials_workspace_id_id_key UNIQUE (workspace_id, id),
    CONSTRAINT model_oauth_credentials_credential_ciphertext_check CHECK (length(credential_ciphertext) > 28),
    CONSTRAINT model_oauth_credentials_provider_check CHECK (provider IN ('chatgpt', 'grok')),
    CONSTRAINT model_oauth_credentials_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE
);

CREATE TABLE nexusdock_auth_flows (
    state_digest BLOB NOT NULL,
    workspace_id TEXT NOT NULL,
    human_account_id TEXT NOT NULL,
    source_id TEXT NOT NULL,
    client_id TEXT NOT NULL,
    code_verifier TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    consumed_at TEXT,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000000Z', 'now')),
    CONSTRAINT nexusdock_auth_flows_pkey PRIMARY KEY (state_digest),
    CONSTRAINT nexusdock_auth_flows_human_account_id_fkey FOREIGN KEY (human_account_id) REFERENCES human_accounts(id) ON DELETE CASCADE,
    CONSTRAINT nexusdock_auth_flows_source_fk FOREIGN KEY (workspace_id, source_id) REFERENCES sources(workspace_id, id) ON DELETE CASCADE,
    CONSTRAINT nexusdock_auth_flows_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE
);

CREATE TABLE nexusdock_oauth_clients (
    resource TEXT NOT NULL,
    client_id TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000000Z', 'now')),
    CONSTRAINT nexusdock_oauth_clients_pkey PRIMARY KEY (resource),
    CONSTRAINT nexusdock_oauth_clients_client_id_key UNIQUE (client_id)
);

CREATE TABLE oauth_authorization_codes (
    code_digest BLOB NOT NULL,
    grant_id TEXT NOT NULL,
    redirect_uri TEXT NOT NULL,
    code_challenge TEXT NOT NULL,
    scope TEXT NOT NULL,
    resource TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    consumed_at TEXT,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000000Z', 'now')),
    CONSTRAINT oauth_authorization_codes_pkey PRIMARY KEY (code_digest),
    CONSTRAINT oauth_authorization_codes_check CHECK (expires_at > created_at),
    CONSTRAINT oauth_authorization_codes_code_challenge_check CHECK (length(code_challenge) >= 43 AND length(code_challenge) <= 128),
    CONSTRAINT oauth_authorization_codes_code_digest_check CHECK (length(code_digest) = 32),
    CONSTRAINT oauth_authorization_codes_redirect_uri_check CHECK (trim(redirect_uri) <> ''),
    CONSTRAINT oauth_authorization_codes_resource_check CHECK (trim(resource) <> ''),
    CONSTRAINT oauth_authorization_codes_scope_check CHECK (trim(scope) <> ''),
    CONSTRAINT oauth_authorization_codes_grant_id_fkey FOREIGN KEY (grant_id) REFERENCES oauth_grants(id) ON DELETE CASCADE
);

CREATE TABLE oauth_clients (
    client_id TEXT NOT NULL,
    client_name TEXT NOT NULL,
    redirect_uris TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000000Z', 'now')),
    CONSTRAINT oauth_clients_pkey PRIMARY KEY (client_id),
    CONSTRAINT oauth_clients_client_id_check CHECK (trim(client_id) <> ''),
    CONSTRAINT oauth_clients_client_name_check CHECK (trim(client_name) <> ''),
    CONSTRAINT oauth_clients_redirect_uris_check CHECK (json_array_length(redirect_uris) >= 1 AND json_array_length(redirect_uris) <= 20)
);

CREATE TABLE oauth_grants (
    id TEXT NOT NULL,
    workspace_id TEXT NOT NULL,
    human_account_id TEXT NOT NULL,
    human_source_id TEXT NOT NULL,
    client_id TEXT NOT NULL,
    source_id TEXT NOT NULL,
    resource TEXT NOT NULL,
    scope TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000000Z', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000000Z', 'now')),
    revoked_at TEXT,
    CONSTRAINT oauth_grants_pkey PRIMARY KEY (id),
    CONSTRAINT oauth_grants_workspace_id_client_id_resource_key UNIQUE (workspace_id, client_id, resource),
    CONSTRAINT oauth_grants_workspace_id_source_id_key UNIQUE (workspace_id, source_id),
    CONSTRAINT oauth_grants_distinct_sources_ck CHECK (human_source_id <> source_id),
    CONSTRAINT oauth_grants_resource_check CHECK (trim(resource) <> ''),
    CONSTRAINT oauth_grants_scope_check CHECK (trim(scope) <> ''),
    CONSTRAINT oauth_grants_client_id_fkey FOREIGN KEY (client_id) REFERENCES oauth_clients(client_id) ON DELETE CASCADE,
    CONSTRAINT oauth_grants_human_membership_fk FOREIGN KEY (workspace_id, human_account_id, human_source_id) REFERENCES workspace_memberships(workspace_id, human_account_id, source_id) ON DELETE CASCADE,
    CONSTRAINT oauth_grants_source_fk FOREIGN KEY (workspace_id, source_id) REFERENCES sources(workspace_id, id) ON DELETE CASCADE,
    CONSTRAINT oauth_grants_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE
);

CREATE TABLE oauth_tokens (
    id TEXT NOT NULL,
    grant_id TEXT NOT NULL,
    family_id TEXT NOT NULL,
    access_token_digest BLOB NOT NULL,
    refresh_token_digest BLOB NOT NULL,
    access_expires_at TEXT NOT NULL,
    refresh_expires_at TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000000Z', 'now')),
    revoked_at TEXT,
    CONSTRAINT oauth_tokens_pkey PRIMARY KEY (id),
    CONSTRAINT oauth_tokens_access_token_digest_key UNIQUE (access_token_digest),
    CONSTRAINT oauth_tokens_refresh_token_digest_key UNIQUE (refresh_token_digest),
    CONSTRAINT oauth_tokens_access_token_digest_check CHECK (length(access_token_digest) = 32),
    CONSTRAINT oauth_tokens_check CHECK (access_expires_at > created_at),
    CONSTRAINT oauth_tokens_check1 CHECK (refresh_expires_at > access_expires_at),
    CONSTRAINT oauth_tokens_refresh_token_digest_check CHECK (length(refresh_token_digest) = 32),
    CONSTRAINT oauth_tokens_grant_id_fkey FOREIGN KEY (grant_id) REFERENCES oauth_grants(id) ON DELETE CASCADE
);

CREATE TABLE oidc_identities (
    issuer TEXT NOT NULL,
    subject TEXT NOT NULL,
    human_account_id TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000000Z', 'now')),
    CONSTRAINT oidc_identities_pkey PRIMARY KEY (issuer, subject),
    CONSTRAINT oidc_identities_issuer_check CHECK (trim(issuer) <> ''),
    CONSTRAINT oidc_identities_subject_check CHECK (trim(subject) <> ''),
    CONSTRAINT oidc_identities_human_account_id_fkey FOREIGN KEY (human_account_id) REFERENCES human_accounts(id) ON DELETE CASCADE
);

CREATE TABLE projects (
    id TEXT NOT NULL,
    workspace_id TEXT NOT NULL,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000000Z', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000000Z', 'now')),
    main_discussion_id TEXT,
    primary_push_mode TEXT NOT NULL DEFAULT 'last_active',
    primary_push_channel_id TEXT,
    CONSTRAINT projects_pkey PRIMARY KEY (id),
    CONSTRAINT projects_workspace_id_id_key UNIQUE (workspace_id, id),
    CONSTRAINT projects_name_check CHECK (trim(name) <> ''),
    CONSTRAINT projects_primary_push_channel_shape_ck CHECK (primary_push_mode = 'channel' OR primary_push_channel_id IS NULL),
    CONSTRAINT projects_primary_push_mode_ck CHECK (primary_push_mode IN ('last_active', 'channel', 'none')),
    CONSTRAINT projects_main_discussion_fk FOREIGN KEY (main_discussion_id) REFERENCES cards(id),
    CONSTRAINT projects_primary_push_channel_fk FOREIGN KEY (primary_push_channel_id) REFERENCES remote_channels(id) ON DELETE SET NULL,
    CONSTRAINT projects_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE
);

CREATE TABLE push_card_revisions (
    workspace_id TEXT NOT NULL,
    card_id TEXT NOT NULL,
    revision TEXT NOT NULL,
    event_sequence TEXT NOT NULL DEFAULT '0',
    message_preview TEXT NOT NULL DEFAULT '',
    attempt_status TEXT NOT NULL DEFAULT '',
    title TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000000Z', 'now')),
    CONSTRAINT push_card_revisions_pkey PRIMARY KEY (workspace_id, card_id),
    CONSTRAINT push_card_revisions_card_fk FOREIGN KEY (workspace_id, card_id) REFERENCES cards(workspace_id, id) ON DELETE CASCADE,
    CONSTRAINT push_card_revisions_workspace_fk FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE
);

CREATE TABLE push_subscription_card_revisions (
    subscription_id TEXT NOT NULL,
    workspace_id TEXT NOT NULL,
    card_id TEXT NOT NULL,
    revision TEXT NOT NULL,
    event_sequence TEXT NOT NULL DEFAULT '0',
    message_preview TEXT NOT NULL DEFAULT '',
    attempt_status TEXT NOT NULL DEFAULT '',
    title TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000000Z', 'now')),
    CONSTRAINT push_subscription_card_revisions_pkey PRIMARY KEY (subscription_id, card_id),
    CONSTRAINT push_subscription_card_revisions_card_fk FOREIGN KEY (workspace_id, card_id) REFERENCES cards(workspace_id, id) ON DELETE CASCADE,
    CONSTRAINT push_subscription_card_revisions_subscription_id_fkey FOREIGN KEY (subscription_id) REFERENCES push_subscriptions(id) ON DELETE CASCADE,
    CONSTRAINT push_subscription_card_revisions_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE
);

CREATE TABLE push_subscriptions (
    id TEXT NOT NULL DEFAULT (lower(hex(randomblob(4)) || '-' || hex(randomblob(2)) || '-4' || substr(hex(randomblob(2)), 2) || '-' || substr('89ab', abs(random()) % 4 + 1, 1) || substr(hex(randomblob(2)), 2) || '-' || hex(randomblob(6)))),
    workspace_id TEXT NOT NULL,
    user_id TEXT NOT NULL,
    endpoint TEXT NOT NULL,
    p256dh TEXT NOT NULL,
    auth_secret TEXT NOT NULL,
    active_until TEXT,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000000Z', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000000Z', 'now')),
    CONSTRAINT push_subscriptions_pkey PRIMARY KEY (id),
    CONSTRAINT push_subscriptions_endpoint_key UNIQUE (endpoint),
    CONSTRAINT push_subscriptions_auth_ck CHECK (length(auth_secret) >= 1 AND length(auth_secret) <= 200),
    CONSTRAINT push_subscriptions_endpoint_ck CHECK (length(endpoint) >= 8 AND length(endpoint) <= 2000 AND substr(endpoint, 1, 8) = 'https://'),
    CONSTRAINT push_subscriptions_p256dh_ck CHECK (length(p256dh) >= 1 AND length(p256dh) <= 200),
    CONSTRAINT push_subscriptions_user_id_fkey FOREIGN KEY (user_id) REFERENCES human_accounts(id) ON DELETE CASCADE,
    CONSTRAINT push_subscriptions_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE
);

CREATE TABLE remote_channel_inbound_dedup (
    id TEXT NOT NULL,
    workspace_id TEXT NOT NULL,
    channel_id TEXT NOT NULL,
    external_message_id TEXT NOT NULL,
    sender_id TEXT NOT NULL DEFAULT '',
    chat_id TEXT NOT NULL DEFAULT '',
    card_id TEXT,
    event_id TEXT,
    turn_id TEXT,
    received_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000000Z', 'now')),
    CONSTRAINT remote_channel_inbound_dedup_pkey PRIMARY KEY (id),
    CONSTRAINT remote_channel_inbound_dedup_uq UNIQUE (workspace_id, channel_id, external_message_id),
    CONSTRAINT remote_channel_inbound_dedup_external_message_id_check CHECK (trim(external_message_id) <> ''),
    CONSTRAINT remote_channel_inbound_dedup_channel_fk FOREIGN KEY (workspace_id, channel_id) REFERENCES remote_channels(workspace_id, id) ON DELETE CASCADE
);

CREATE TABLE remote_channel_outbound_cursors (
    workspace_id TEXT NOT NULL,
    channel_id TEXT NOT NULL,
    card_id TEXT NOT NULL,
    last_message_seq INTEGER NOT NULL DEFAULT 0,
    last_comment_seq INTEGER NOT NULL DEFAULT 0,
    last_checkin_seq INTEGER NOT NULL DEFAULT 0,
    last_status_seq INTEGER NOT NULL DEFAULT 0,
    last_attempt_id TEXT,
    last_attempt_status TEXT NOT NULL DEFAULT '',
    last_approval_id TEXT,
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000000Z', 'now')),
    CONSTRAINT remote_channel_outbound_cursors_pkey PRIMARY KEY (workspace_id, channel_id, card_id),
    CONSTRAINT remote_channel_outbound_cursors_card_fk FOREIGN KEY (workspace_id, card_id) REFERENCES cards(workspace_id, id) ON DELETE CASCADE,
    CONSTRAINT remote_channel_outbound_cursors_channel_fk FOREIGN KEY (workspace_id, channel_id) REFERENCES remote_channels(workspace_id, id) ON DELETE CASCADE
);

CREATE TABLE remote_channels (
    id TEXT NOT NULL,
    workspace_id TEXT NOT NULL,
    name TEXT NOT NULL,
    platform TEXT NOT NULL,
    role TEXT NOT NULL,
    transport TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1,
    project_id TEXT NOT NULL,
    target_card_id TEXT,
    push_filter TEXT NOT NULL DEFAULT 'all',
    mirror_web_messages INTEGER NOT NULL DEFAULT 0,
    group_policy TEXT NOT NULL DEFAULT 'mention_only',
    allow_from TEXT NOT NULL DEFAULT '[]',
    config_json TEXT NOT NULL DEFAULT '{}',
    secret_ciphertext BLOB,
    secret_hints TEXT NOT NULL DEFAULT '{}',
    runtime_status TEXT NOT NULL DEFAULT 'disconnected',
    last_error TEXT NOT NULL DEFAULT '',
    last_heartbeat_at TEXT,
    last_inbound_at TEXT,
    last_outbound_at TEXT,
    runtime_state_json TEXT NOT NULL DEFAULT '{}',
    created_by_source_id TEXT,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000000Z', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000000Z', 'now')),
    conversation_card_id TEXT,
    CONSTRAINT remote_channels_pkey PRIMARY KEY (id),
    CONSTRAINT remote_channels_workspace_id_id_key UNIQUE (workspace_id, id),
    CONSTRAINT remote_channels_group_policy_check CHECK (group_policy IN ('mention_only', 'allowlist', 'all')),
    CONSTRAINT remote_channels_name_check CHECK (trim(name) <> ''),
    CONSTRAINT remote_channels_platform_check CHECK (platform IN ('weixin', 'wecom', 'feishu', 'dingtalk', 'qq', 'telegram')),
    CONSTRAINT remote_channels_platform_transport_ck CHECK (platform = 'weixin' AND transport = 'long_poll' OR platform = 'wecom' AND (transport IN ('websocket', 'webhook')) OR platform = 'feishu' AND (transport IN ('websocket', 'webhook')) OR platform = 'dingtalk' AND (transport IN ('stream', 'webhook')) OR platform = 'qq' AND transport = 'websocket' OR platform = 'telegram' AND transport = 'long_poll'),
    CONSTRAINT remote_channels_push_filter_check CHECK (push_filter IN ('all', 'warnings_only')),
    CONSTRAINT remote_channels_role_check CHECK (role IN ('main_chat', 'scheduled_push')),
    CONSTRAINT remote_channels_role_shape_ck CHECK (role = 'main_chat' AND target_card_id IS NULL AND transport <> 'webhook' OR role = 'scheduled_push'),
    CONSTRAINT remote_channels_runtime_status_check CHECK (runtime_status IN ('disconnected', 'connecting', 'connected', 'error')),
    CONSTRAINT remote_channels_transport_check CHECK (transport IN ('long_poll', 'websocket', 'stream', 'webhook')),
    CONSTRAINT remote_channels_card_fk FOREIGN KEY (workspace_id, target_card_id) REFERENCES cards(workspace_id, id) ON DELETE NO ACTION,
    CONSTRAINT remote_channels_conversation_card_fk FOREIGN KEY (conversation_card_id) REFERENCES cards(id) ON DELETE SET NULL,
    CONSTRAINT remote_channels_creator_fk FOREIGN KEY (workspace_id, created_by_source_id) REFERENCES sources(workspace_id, id) ON DELETE NO ACTION,
    CONSTRAINT remote_channels_project_fk FOREIGN KEY (workspace_id, project_id) REFERENCES projects(workspace_id, id) ON DELETE CASCADE,
    CONSTRAINT remote_channels_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE
);

CREATE TABLE sources (
    id TEXT NOT NULL,
    workspace_id TEXT NOT NULL,
    kind TEXT NOT NULL,
    name TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000000Z', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000000Z', 'now')),
    CONSTRAINT sources_pkey PRIMARY KEY (id),
    CONSTRAINT sources_workspace_id_id_key UNIQUE (workspace_id, id),
    CONSTRAINT sources_kind_check CHECK (kind IN ('human', 'cycle_agent', 'oauth_client')),
    CONSTRAINT sources_name_check CHECK (trim(name) <> ''),
    CONSTRAINT sources_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE
);

CREATE TABLE trigger_deliveries (
    id TEXT NOT NULL,
    workspace_id TEXT NOT NULL,
    trigger_id TEXT NOT NULL,
    delivery_id TEXT NOT NULL,
    provider TEXT NOT NULL,
    event_type TEXT NOT NULL DEFAULT '',
    event_action TEXT NOT NULL DEFAULT '',
    signature_valid INTEGER NOT NULL,
    status TEXT NOT NULL,
    reason TEXT NOT NULL DEFAULT '',
    match_keys TEXT NOT NULL DEFAULT '[]',
    payload_summary TEXT NOT NULL DEFAULT '',
    triggered_turn_id TEXT,
    triggered_watcher_turn_ids TEXT NOT NULL DEFAULT '[]',
    created_card_id TEXT,
    received_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000000Z', 'now')),
    CONSTRAINT trigger_deliveries_pkey PRIMARY KEY (id),
    CONSTRAINT trigger_deliveries_delivery_id_check CHECK (trim(delivery_id) <> ''),
    CONSTRAINT trigger_deliveries_provider_check CHECK (provider IN ('github', 'circleci', 'generic')),
    CONSTRAINT trigger_deliveries_status_check CHECK (status IN ('triggered', 'filtered', 'ignored', 'cooldown', 'rejected')),
    CONSTRAINT trigger_deliveries_card_fk FOREIGN KEY (workspace_id, created_card_id) REFERENCES cards(workspace_id, id) ON DELETE NO ACTION,
    CONSTRAINT trigger_deliveries_trigger_fk FOREIGN KEY (workspace_id, trigger_id) REFERENCES workspace_triggers(workspace_id, id) ON DELETE CASCADE,
    CONSTRAINT trigger_deliveries_turn_fk FOREIGN KEY (workspace_id, triggered_turn_id) REFERENCES card_turns(workspace_id, id) ON DELETE NO ACTION,
    CONSTRAINT trigger_deliveries_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE
);

CREATE TABLE turn_attempts (
    id TEXT NOT NULL,
    workspace_id TEXT NOT NULL,
    card_id TEXT NOT NULL,
    turn_id TEXT NOT NULL,
    attempt_number INTEGER NOT NULL,
    reason TEXT NOT NULL,
    retry_of_attempt_id TEXT,
    runner_source_id TEXT NOT NULL,
    invocation_id TEXT,
    status TEXT NOT NULL,
    current_stage TEXT NOT NULL DEFAULT '',
    progress_summary TEXT NOT NULL DEFAULT '',
    error_summary TEXT NOT NULL DEFAULT '',
    started_at TEXT,
    heartbeat_at TEXT,
    lease_expires_at TEXT,
    finished_at TEXT,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000000Z', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000000Z', 'now')),
    CONSTRAINT turn_attempts_pkey PRIMARY KEY (id),
    CONSTRAINT turn_attempts_workspace_id_card_id_id_key UNIQUE (workspace_id, card_id, id),
    CONSTRAINT turn_attempts_workspace_id_id_key UNIQUE (workspace_id, id),
    CONSTRAINT turn_attempts_workspace_id_turn_id_attempt_number_key UNIQUE (workspace_id, turn_id, attempt_number),
    CONSTRAINT turn_attempts_attempt_number_check CHECK (attempt_number > 0),
    CONSTRAINT turn_attempts_invocation_id_check CHECK (invocation_id IS NULL OR trim(invocation_id) <> ''),
    CONSTRAINT turn_attempts_lifecycle_ck CHECK (status = 'queued' AND started_at IS NULL AND heartbeat_at IS NULL AND lease_expires_at IS NULL AND finished_at IS NULL OR status = 'running' AND started_at IS NOT NULL AND heartbeat_at IS NOT NULL AND lease_expires_at IS NOT NULL AND finished_at IS NULL OR (status IN ('completed', 'failed', 'superseded', 'expired', 'cancelled')) AND finished_at IS NOT NULL AND heartbeat_at IS NULL AND lease_expires_at IS NULL),
    CONSTRAINT turn_attempts_reason_check CHECK (reason IN ('initial', 'retry', 'recovery')),
    CONSTRAINT turn_attempts_reason_shape_ck CHECK (reason = 'initial' AND retry_of_attempt_id IS NULL AND attempt_number = 1 OR (reason IN ('retry', 'recovery')) AND retry_of_attempt_id IS NOT NULL AND attempt_number > 1),
    CONSTRAINT turn_attempts_status_check CHECK (status IN ('queued', 'running', 'completed', 'failed', 'superseded', 'expired', 'cancelled')),
    CONSTRAINT turn_attempts_card_fk FOREIGN KEY (workspace_id, card_id) REFERENCES cards(workspace_id, id) ON DELETE CASCADE,
    CONSTRAINT turn_attempts_retry_of_fk FOREIGN KEY (workspace_id, card_id, retry_of_attempt_id) REFERENCES turn_attempts(workspace_id, card_id, id) ON DELETE RESTRICT,
    CONSTRAINT turn_attempts_runner_fk FOREIGN KEY (workspace_id, runner_source_id) REFERENCES sources(workspace_id, id),
    CONSTRAINT turn_attempts_turn_fk FOREIGN KEY (workspace_id, card_id, turn_id) REFERENCES card_turns(workspace_id, card_id, id) ON DELETE CASCADE,
    CONSTRAINT turn_attempts_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE
);

CREATE TABLE web_auth_flows (
    state_digest BLOB NOT NULL,
    provider TEXT NOT NULL,
    code_verifier TEXT NOT NULL,
    nonce TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    consumed_at TEXT,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000000Z', 'now')),
    CONSTRAINT web_auth_flows_pkey PRIMARY KEY (state_digest),
    CONSTRAINT web_auth_flows_code_verifier_check CHECK (trim(code_verifier) <> ''),
    CONSTRAINT web_auth_flows_nonce_check CHECK (trim(nonce) <> ''),
    CONSTRAINT web_auth_flows_provider_check CHECK (provider = 'oidc'),
    CONSTRAINT web_auth_flows_state_digest_check CHECK (length(state_digest) = 32)
);

CREATE TABLE web_sessions (
    id TEXT NOT NULL,
    human_account_id TEXT NOT NULL,
    workspace_id TEXT NOT NULL,
    source_id TEXT NOT NULL,
    token_digest BLOB NOT NULL,
    expires_at TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000000Z', 'now')),
    CONSTRAINT web_sessions_pkey PRIMARY KEY (id),
    CONSTRAINT web_sessions_token_digest_key UNIQUE (token_digest),
    CONSTRAINT web_sessions_token_digest_check CHECK (length(token_digest) = 32),
    CONSTRAINT web_sessions_membership_fk FOREIGN KEY (workspace_id, human_account_id, source_id) REFERENCES workspace_memberships(workspace_id, human_account_id, source_id) ON DELETE CASCADE
);

CREATE TABLE workspace_agent_settings (
    workspace_id TEXT NOT NULL,
    source_id TEXT NOT NULL,
    system_instructions TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000000Z', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000000Z', 'now')),
    default_model_route_id TEXT,
    discussion_model_route_id TEXT,
    plan_model_route_id TEXT,
    tool_guard_mode TEXT NOT NULL DEFAULT 'block_high_risk',
    tool_guard_model_route_id TEXT,
    tool_guard_timeout_seconds INTEGER NOT NULL DEFAULT 10,
    tool_guard_custom_rules TEXT NOT NULL DEFAULT '',
    CONSTRAINT workspace_agent_settings_pkey PRIMARY KEY (workspace_id),
    CONSTRAINT workspace_agent_settings_source_id_key UNIQUE (source_id),
    CONSTRAINT workspace_agent_settings_workspace_id_source_id_key UNIQUE (workspace_id, source_id),
    CONSTRAINT workspace_agent_settings_tool_guard_mode_ck CHECK (tool_guard_mode IN ('block_high_risk', 'strict')),
    CONSTRAINT workspace_agent_settings_tool_guard_timeout_ck CHECK (tool_guard_timeout_seconds >= 2 AND tool_guard_timeout_seconds <= 60),
    CONSTRAINT workspace_agent_settings_default_model_route_id_fkey FOREIGN KEY (default_model_route_id) REFERENCES workspace_model_routes(id) ON DELETE SET NULL,
    CONSTRAINT workspace_agent_settings_discussion_model_route_id_fkey FOREIGN KEY (discussion_model_route_id) REFERENCES workspace_model_routes(id) ON DELETE SET NULL,
    CONSTRAINT workspace_agent_settings_plan_model_route_id_fkey FOREIGN KEY (plan_model_route_id) REFERENCES workspace_model_routes(id) ON DELETE SET NULL,
    CONSTRAINT workspace_agent_settings_source_fk FOREIGN KEY (workspace_id, source_id) REFERENCES sources(workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT workspace_agent_settings_tool_guard_model_route_id_fkey FOREIGN KEY (tool_guard_model_route_id) REFERENCES workspace_model_routes(id) ON DELETE SET NULL,
    CONSTRAINT workspace_agent_settings_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE
);

CREATE TABLE workspace_api_credentials (
    id TEXT NOT NULL,
    workspace_id TEXT NOT NULL,
    name TEXT NOT NULL DEFAULT '',
    base_url TEXT NOT NULL,
    api_key_env TEXT NOT NULL DEFAULT '',
    api_key_ciphertext BLOB,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000000Z', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000000Z', 'now')),
    CONSTRAINT workspace_api_credentials_pkey PRIMARY KEY (id),
    CONSTRAINT workspace_api_credentials_workspace_id_id_key UNIQUE (workspace_id, id),
    CONSTRAINT workspace_api_credentials_base_url_ck CHECK (trim(base_url) <> ''),
    CONSTRAINT workspace_api_credentials_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE
);

CREATE TABLE workspace_attachments (
    id TEXT NOT NULL,
    workspace_id TEXT NOT NULL,
    card_id TEXT,
    uploaded_by_source_id TEXT NOT NULL,
    mime_type TEXT NOT NULL,
    size_bytes INTEGER NOT NULL,
    width INTEGER NOT NULL,
    height INTEGER NOT NULL,
    sha256 TEXT NOT NULL,
    relative_path TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000000Z', 'now')),
    expires_at TEXT NOT NULL,
    purged_at TEXT,
    kind TEXT NOT NULL DEFAULT 'image',
    file_name TEXT NOT NULL DEFAULT '',
    storage_encoding TEXT NOT NULL DEFAULT 'identity',
    stored_size_bytes INTEGER NOT NULL DEFAULT 0,
    line_count INTEGER NOT NULL DEFAULT 0,
    CONSTRAINT workspace_attachments_pkey PRIMARY KEY (id),
    CONSTRAINT workspace_attachments_dimensions_check CHECK (kind = 'image' AND width > 0 AND height > 0 OR kind = 'text' AND width = 0 AND height = 0),
    CONSTRAINT workspace_attachments_kind_check CHECK (kind IN ('image', 'text')),
    CONSTRAINT workspace_attachments_line_count_check CHECK (line_count >= 0),
    CONSTRAINT workspace_attachments_relative_path_check CHECK (trim(relative_path) <> ''),
    CONSTRAINT workspace_attachments_sha256_check CHECK (trim(sha256) <> ''),
    CONSTRAINT workspace_attachments_size_bytes_check CHECK (size_bytes > 0),
    CONSTRAINT workspace_attachments_storage_encoding_check CHECK (storage_encoding IN ('identity', 'gzip')),
    CONSTRAINT workspace_attachments_stored_size_bytes_check CHECK (stored_size_bytes > 0),
    CONSTRAINT workspace_attachments_card_fk FOREIGN KEY (workspace_id, card_id) REFERENCES cards(workspace_id, id) ON DELETE NO ACTION,
    CONSTRAINT workspace_attachments_source_fk FOREIGN KEY (workspace_id, uploaded_by_source_id) REFERENCES sources(workspace_id, id) ON DELETE RESTRICT,
    CONSTRAINT workspace_attachments_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE
);

CREATE TABLE workspace_device_tool_guards (
    id TEXT NOT NULL,
    workspace_id TEXT NOT NULL,
    device_node_id TEXT NOT NULL,
    device_name TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000000Z', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000000Z', 'now')),
    enabled INTEGER NOT NULL DEFAULT 0,
    CONSTRAINT workspace_device_tool_guards_pkey PRIMARY KEY (id),
    CONSTRAINT workspace_device_tool_guards_node_uq UNIQUE (workspace_id, device_node_id),
    CONSTRAINT workspace_device_tool_guards_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE
);

CREATE TABLE workspace_mcp_connections (
    id TEXT NOT NULL,
    workspace_id TEXT NOT NULL,
    name TEXT NOT NULL,
    url TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1,
    timeout_ms INTEGER NOT NULL DEFAULT 30000,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000000Z', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000000Z', 'now')),
    bearer_token_ciphertext BLOB,
    tool_guard_enabled INTEGER NOT NULL DEFAULT 0,
    CONSTRAINT workspace_mcp_connections_pkey PRIMARY KEY (id),
    CONSTRAINT workspace_mcp_connections_workspace_id_id_key UNIQUE (workspace_id, id),
    CONSTRAINT workspace_mcp_connections_workspace_id_name_key UNIQUE (workspace_id, name),
    CONSTRAINT workspace_mcp_connections_name_check CHECK (trim(name) <> ''),
    CONSTRAINT workspace_mcp_connections_timeout_ms_check CHECK (timeout_ms >= 1000 AND timeout_ms <= 300000),
    CONSTRAINT workspace_mcp_connections_url_check CHECK (trim(url) <> ''),
    CONSTRAINT workspace_mcp_connections_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE
);

CREATE TABLE workspace_memberships (
    workspace_id TEXT NOT NULL,
    human_account_id TEXT NOT NULL,
    source_id TEXT NOT NULL,
    role TEXT NOT NULL,
    is_personal INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000000Z', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000000Z', 'now')),
    CONSTRAINT workspace_memberships_pkey PRIMARY KEY (workspace_id, human_account_id),
    CONSTRAINT workspace_memberships_source_id_key UNIQUE (source_id),
    CONSTRAINT workspace_memberships_workspace_id_human_account_id_source__key UNIQUE (workspace_id, human_account_id, source_id),
    CONSTRAINT workspace_memberships_role_check CHECK (role IN ('owner', 'member')),
    CONSTRAINT workspace_memberships_human_account_id_fkey FOREIGN KEY (human_account_id) REFERENCES human_accounts(id) ON DELETE CASCADE,
    CONSTRAINT workspace_memberships_source_fk FOREIGN KEY (workspace_id, source_id) REFERENCES sources(workspace_id, id) ON DELETE CASCADE,
    CONSTRAINT workspace_memberships_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE
);

CREATE TABLE workspace_model_routes (
    id TEXT NOT NULL,
    workspace_id TEXT NOT NULL,
    position INTEGER NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1,
    provider_type TEXT NOT NULL,
    credential_id TEXT,
    model TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000000Z', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000000Z', 'now')),
    base_model TEXT NOT NULL DEFAULT '',
    context_window_tokens INTEGER,
    max_input_tokens INTEGER,
    max_output_tokens INTEGER,
    limits_source TEXT NOT NULL DEFAULT 'unknown',
    limits_verified_at TEXT,
    api_credential_id TEXT,
    CONSTRAINT workspace_model_routes_pkey PRIMARY KEY (id),
    CONSTRAINT workspace_model_routes_workspace_id_id_key UNIQUE (workspace_id, id),
    CONSTRAINT workspace_model_routes_workspace_id_position_key UNIQUE (workspace_id, "position"),
    CONSTRAINT workspace_model_routes_limits_bounds_check CHECK ((context_window_tokens IS NULL OR max_input_tokens IS NULL OR max_input_tokens <= context_window_tokens) AND (context_window_tokens IS NULL OR max_output_tokens IS NULL OR max_output_tokens <= context_window_tokens)),
    CONSTRAINT workspace_model_routes_limits_positive_check CHECK ((context_window_tokens IS NULL OR context_window_tokens > 0) AND (max_input_tokens IS NULL OR max_input_tokens > 0) AND (max_output_tokens IS NULL OR max_output_tokens > 0)),
    CONSTRAINT workspace_model_routes_limits_source_check CHECK (limits_source IN ('user', 'provider', 'registry', 'unknown')),
    CONSTRAINT workspace_model_routes_model_check CHECK (trim(model) <> ''),
    CONSTRAINT workspace_model_routes_position_check CHECK ("position" >= 0),
    CONSTRAINT workspace_model_routes_provider_ck CHECK (provider_type = 'openai_compatible' AND credential_id IS NULL AND api_credential_id IS NOT NULL OR (provider_type IN ('chatgpt', 'grok')) AND api_credential_id IS NULL AND (NOT enabled OR credential_id IS NOT NULL)),
    CONSTRAINT workspace_model_routes_provider_type_check CHECK (provider_type IN ('openai_compatible', 'chatgpt', 'grok')),
    CONSTRAINT workspace_model_routes_api_credential_fk FOREIGN KEY (workspace_id, api_credential_id) REFERENCES workspace_api_credentials(workspace_id, id) ON DELETE CASCADE,
    CONSTRAINT workspace_model_routes_credential_fk FOREIGN KEY (workspace_id, credential_id) REFERENCES model_oauth_credentials(workspace_id, id) ON DELETE CASCADE,
    CONSTRAINT workspace_model_routes_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE
);

CREATE TABLE workspace_nexusdock_connections (
    workspace_id TEXT NOT NULL,
    resource TEXT NOT NULL,
    client_id TEXT NOT NULL,
    access_token_ciphertext BLOB NOT NULL,
    refresh_token_ciphertext BLOB NOT NULL,
    access_expires_at TEXT NOT NULL,
    connected_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000000Z', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000000Z', 'now')),
    authorized_subject TEXT NOT NULL DEFAULT '',
    CONSTRAINT workspace_nexusdock_connections_pkey PRIMARY KEY (workspace_id),
    CONSTRAINT workspace_nexusdock_connections_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE
);

CREATE TABLE workspace_triggers (
    id TEXT NOT NULL,
    workspace_id TEXT NOT NULL,
    name TEXT NOT NULL,
    provider TEXT NOT NULL,
    auth_mode TEXT NOT NULL,
    signature_header TEXT NOT NULL DEFAULT '',
    secret_ciphertext BLOB NOT NULL,
    secret_hint TEXT NOT NULL DEFAULT '',
    enabled INTEGER NOT NULL DEFAULT 1,
    routing_mode TEXT NOT NULL,
    target_project_id TEXT,
    target_card_id TEXT,
    dispatch_mode TEXT NOT NULL DEFAULT 'enqueue',
    filter_config TEXT NOT NULL DEFAULT '{}',
    cooldown_seconds INTEGER NOT NULL DEFAULT 0,
    last_triggered_at TEXT,
    created_by_source_id TEXT,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000000Z', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000000Z', 'now')),
    CONSTRAINT workspace_triggers_pkey PRIMARY KEY (id),
    CONSTRAINT workspace_triggers_workspace_id_id_key UNIQUE (workspace_id, id),
    CONSTRAINT workspace_triggers_auth_mode_check CHECK (auth_mode IN ('github_hmac_sha256', 'circleci_hmac_sha256', 'generic_hmac_sha256', 'bearer')),
    CONSTRAINT workspace_triggers_cooldown_seconds_check CHECK (cooldown_seconds >= 0 AND cooldown_seconds <= 86400),
    CONSTRAINT workspace_triggers_dispatch_mode_check CHECK (dispatch_mode IN ('enqueue', 'interrupt')),
    CONSTRAINT workspace_triggers_name_check CHECK (trim(name) <> ''),
    CONSTRAINT workspace_triggers_provider_auth_ck CHECK (provider = 'github' AND auth_mode = 'github_hmac_sha256' OR provider = 'circleci' AND auth_mode = 'circleci_hmac_sha256' OR provider = 'generic' AND (auth_mode IN ('generic_hmac_sha256', 'bearer'))),
    CONSTRAINT workspace_triggers_provider_check CHECK (provider IN ('github', 'circleci', 'generic')),
    CONSTRAINT workspace_triggers_routing_mode_check CHECK (routing_mode IN ('fixed_card', 'match_watchers', 'create_todo')),
    CONSTRAINT workspace_triggers_routing_shape_ck CHECK (routing_mode = 'fixed_card' AND target_card_id IS NOT NULL OR routing_mode = 'create_todo' AND target_project_id IS NOT NULL AND target_card_id IS NULL OR routing_mode = 'match_watchers' AND target_card_id IS NULL),
    CONSTRAINT workspace_triggers_secret_ciphertext_check CHECK (length(secret_ciphertext) > 0),
    CONSTRAINT workspace_triggers_card_fk FOREIGN KEY (workspace_id, target_card_id) REFERENCES cards(workspace_id, id) ON DELETE CASCADE,
    CONSTRAINT workspace_triggers_project_fk FOREIGN KEY (workspace_id, target_project_id) REFERENCES projects(workspace_id, id) ON DELETE CASCADE,
    CONSTRAINT workspace_triggers_source_fk FOREIGN KEY (workspace_id, created_by_source_id) REFERENCES sources(workspace_id, id) ON DELETE NO ACTION,
    CONSTRAINT workspace_triggers_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE
);

CREATE TABLE workspaces (
    id TEXT NOT NULL,
    name TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000000Z', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f000000Z', 'now')),
    CONSTRAINT workspaces_pkey PRIMARY KEY (id),
    CONSTRAINT workspaces_name_check CHECK (trim(name) <> '')
);

-- 索引与部分唯一索引
CREATE INDEX agent_artifacts_list_idx ON agent_artifacts (app_name, user_id, session_id, file_name, version DESC);
CREATE INDEX agent_artifacts_retention_idx ON agent_artifacts (app_name, user_id, session_id, created_at) WHERE (mime_type NOT LIKE 'image/%');
CREATE INDEX agent_model_calls_attempt_idx ON agent_model_calls (workspace_id, card_id, attempt_id, call_index);
CREATE INDEX agent_session_events_compaction_idx ON agent_session_events (app_name, user_id, session_id, occurred_at DESC, sequence DESC) WHERE (json_extract(event, '$.actions.compaction') IS NOT NULL);
CREATE INDEX agent_session_events_history_idx ON agent_session_events (app_name, user_id, session_id, occurred_at, id);
CREATE INDEX agent_sessions_user_updated_idx ON agent_sessions (app_name, user_id, updated_at DESC);
CREATE INDEX card_attachments_card_position_idx ON card_attachments (workspace_id, card_id, "position", created_at);
CREATE INDEX card_read_cursors_card_idx ON card_read_cursors (workspace_id, card_id);
CREATE INDEX card_schedules_active_card_idx ON card_schedules (workspace_id, card_id, created_at, id) WHERE (archived_at IS NULL);
CREATE INDEX card_schedules_card_updated_idx ON card_schedules (workspace_id, card_id, updated_at DESC);
CREATE INDEX card_schedules_due_idx ON card_schedules (next_trigger_at) WHERE (enabled AND (archived_at IS NULL));
CREATE INDEX idx_card_tool_approvals_card_status ON card_tool_approvals (workspace_id, card_id, status);
CREATE UNIQUE INDEX idx_card_tool_whitelists_scope_uq ON card_tool_whitelists (workspace_id, card_id, scope_type, scope_target);
CREATE INDEX card_turns_cancelled_source_event_idx ON card_turns (workspace_id, card_id, source_event_id) WHERE ((cancelled_at IS NOT NULL) AND (source_event_id IS NOT NULL));
CREATE INDEX card_turns_card_sequence_idx ON card_turns (workspace_id, card_id, sequence DESC);
CREATE INDEX card_turns_pending_idx ON card_turns (workspace_id, sequence) WHERE (cancelled_at IS NULL);
CREATE UNIQUE INDEX card_turns_schedule_occurrence_uq ON card_turns (schedule_id, scheduled_for) WHERE (schedule_id IS NOT NULL);
CREATE UNIQUE INDEX card_turns_source_idempotency_uq ON card_turns (workspace_id, created_by_source_id, idempotency_key) WHERE ((created_by_source_id IS NOT NULL) AND (idempotency_key IS NOT NULL));
CREATE UNIQUE INDEX card_turns_trigger_idempotency_uq ON card_turns (workspace_id, idempotency_key) WHERE ((source = 'trigger') AND (idempotency_key IS NOT NULL));
CREATE INDEX card_watchers_card_created_idx ON card_watchers (workspace_id, card_id, created_at DESC);
CREATE INDEX card_watchers_due_armed_idx ON card_watchers (workspace_id, expires_at) WHERE (status = 'armed');
CREATE UNIQUE INDEX card_watchers_idempotency_uq ON card_watchers (workspace_id, card_id, idempotency_key) WHERE (idempotency_key IS NOT NULL);
CREATE INDEX card_watchers_workspace_match_armed_idx ON card_watchers (workspace_id, lower(match_key)) WHERE ((status = 'armed') AND (kind = 'event'));
CREATE INDEX cards_pinned_idx ON cards (workspace_id, project_id, pinned_at DESC);
CREATE INDEX cards_project_idx ON cards (workspace_id, project_id, created_at);
CREATE UNIQUE INDEX cards_source_create_idempotency_uq ON cards (workspace_id, created_by_source_id, create_idempotency_key) WHERE (create_idempotency_key IS NOT NULL);
CREATE INDEX idx_cards_preferred_model_route ON cards (preferred_model_route_id);
CREATE INDEX events_attempt_idx ON events (workspace_id, card_id, attempt_id, sequence) WHERE (attempt_id IS NOT NULL);
CREATE INDEX events_card_notifiable_sequence_idx ON events (workspace_id, card_id, sequence DESC) WHERE (event_type IN ('message', 'tool_approval'));
CREATE INDEX events_card_sequence_idx ON events (workspace_id, card_id, sequence);
CREATE INDEX events_card_visible_messages_idx ON events (workspace_id, card_id, sequence DESC) WHERE (event_type IN ('comment', 'message'));
CREATE UNIQUE INDEX events_comment_idempotency_uq ON events (workspace_id, actor_source_id, idempotency_key) WHERE ((event_type = 'comment') AND (idempotency_key IS NOT NULL));
CREATE UNIQUE INDEX events_confirm_source_uq ON events (workspace_id, card_id, actor_source_id, confirmation_round) WHERE (event_type = 'confirm');
CREATE UNIQUE INDEX events_message_idempotency_uq ON events (workspace_id, card_id, attempt_id, actor_source_id, idempotency_key) WHERE ((event_type = 'message') AND (attempt_id IS NOT NULL) AND (idempotency_key IS NOT NULL));
CREATE UNIQUE INDEX events_reply_preview_key ON events (workspace_id, card_id, attempt_id, json_extract(message, '$.preview_id')) WHERE (json_type(message, '$.preview_id') IS NOT NULL);
CREATE UNIQUE INDEX events_source_type_idempotency_uq ON events (workspace_id, actor_source_id, event_type, idempotency_key) WHERE ((idempotency_key IS NOT NULL) AND (event_type IN ('progress', 'finding', 'checkin', 'confirm')));
CREATE UNIQUE INDEX human_accounts_single_local_admin_uq ON human_accounts (is_local_admin) WHERE is_local_admin;
CREATE UNIQUE INDEX model_auth_flows_state_idx ON model_auth_flows (state_digest) WHERE (state_digest IS NOT NULL);
CREATE INDEX model_auth_flows_workspace_idx ON model_auth_flows (workspace_id, provider, expires_at);
CREATE UNIQUE INDEX model_oauth_credentials_account_identity_idx ON model_oauth_credentials (workspace_id, provider, account_subject) WHERE (trim(account_subject) <> '');
CREATE INDEX nexusdock_auth_flows_expiry_idx ON nexusdock_auth_flows (expires_at);
CREATE INDEX oauth_authorization_codes_expiry_idx ON oauth_authorization_codes (expires_at);
CREATE INDEX oauth_grants_workspace_client_idx ON oauth_grants (workspace_id, client_id);
CREATE INDEX oauth_tokens_access_expiry_idx ON oauth_tokens (access_expires_at);
CREATE INDEX oauth_tokens_family_idx ON oauth_tokens (family_id);
CREATE INDEX oauth_tokens_refresh_expiry_idx ON oauth_tokens (refresh_expires_at);
CREATE INDEX oidc_identities_human_account_idx ON oidc_identities (human_account_id);
CREATE UNIQUE INDEX projects_main_discussion_uq ON projects (main_discussion_id) WHERE (main_discussion_id IS NOT NULL);
CREATE INDEX projects_primary_push_channel_idx ON projects (workspace_id, primary_push_channel_id) WHERE (primary_push_channel_id IS NOT NULL);
CREATE INDEX projects_workspace_idx ON projects (workspace_id, created_at);
CREATE INDEX push_subscription_card_revisions_workspace_idx ON push_subscription_card_revisions (workspace_id, subscription_id);
CREATE INDEX push_subscriptions_workspace_idx ON push_subscriptions (workspace_id);
CREATE INDEX remote_channel_inbound_dedup_event_idx ON remote_channel_inbound_dedup (workspace_id, event_id) WHERE (event_id IS NOT NULL);
CREATE INDEX remote_channel_inbound_dedup_received_idx ON remote_channel_inbound_dedup (workspace_id, channel_id, received_at DESC);
CREATE INDEX remote_channel_inbound_dedup_turn_idx ON remote_channel_inbound_dedup (workspace_id, turn_id) WHERE (turn_id IS NOT NULL);
CREATE INDEX remote_channels_conversation_card_idx ON remote_channels (workspace_id, conversation_card_id) WHERE (conversation_card_id IS NOT NULL);
CREATE INDEX remote_channels_enabled_idx ON remote_channels (workspace_id, role, project_id) WHERE (enabled = true);
CREATE INDEX remote_channels_workspace_idx ON remote_channels (workspace_id, created_at DESC, id);
CREATE INDEX trigger_deliveries_trigger_received_idx ON trigger_deliveries (workspace_id, trigger_id, received_at DESC);
CREATE UNIQUE INDEX trigger_deliveries_verified_dedup_uq ON trigger_deliveries (trigger_id, delivery_id) WHERE (signature_valid = true);
CREATE INDEX turn_attempts_card_latest_idx ON turn_attempts (workspace_id, card_id, created_at DESC, attempt_number DESC, id DESC);
CREATE UNIQUE INDEX turn_attempts_invocation_uq ON turn_attempts (workspace_id, invocation_id) WHERE (invocation_id IS NOT NULL);
CREATE UNIQUE INDEX turn_attempts_one_current_per_card_uq ON turn_attempts (workspace_id, card_id) WHERE (status IN ('queued', 'running'));
CREATE UNIQUE INDEX turn_attempts_one_successor_uq ON turn_attempts (retry_of_attempt_id) WHERE (retry_of_attempt_id IS NOT NULL);
CREATE INDEX turn_attempts_runner_running_idx ON turn_attempts (workspace_id, runner_source_id) WHERE (status = 'running');
CREATE INDEX turn_attempts_runner_started_idx ON turn_attempts (workspace_id, runner_source_id, started_at DESC) WHERE (started_at IS NOT NULL);
CREATE INDEX turn_attempts_turn_history_idx ON turn_attempts (workspace_id, turn_id, attempt_number);
CREATE INDEX web_auth_flows_expiry_idx ON web_auth_flows (expires_at);
CREATE INDEX web_sessions_expiry_idx ON web_sessions (expires_at);
CREATE INDEX idx_workspace_agent_settings_default_route ON workspace_agent_settings (default_model_route_id);
CREATE INDEX idx_workspace_agent_settings_discussion_route ON workspace_agent_settings (discussion_model_route_id);
CREATE INDEX idx_workspace_agent_settings_guard_route ON workspace_agent_settings (tool_guard_model_route_id);
CREATE INDEX idx_workspace_agent_settings_plan_route ON workspace_agent_settings (plan_model_route_id);
CREATE INDEX idx_workspace_api_credentials_workspace ON workspace_api_credentials (workspace_id, created_at, id);
CREATE INDEX workspace_attachments_active_created_idx ON workspace_attachments (created_at, id) WHERE (purged_at IS NULL);
CREATE INDEX workspace_attachments_active_expires_idx ON workspace_attachments (expires_at, id) WHERE (purged_at IS NULL);
CREATE INDEX workspace_attachments_ws_card_idx ON workspace_attachments (workspace_id, card_id) WHERE (card_id IS NOT NULL);
CREATE INDEX workspace_attachments_ws_sha256_idx ON workspace_attachments (workspace_id, kind, sha256) WHERE (purged_at IS NULL);
CREATE INDEX idx_workspace_device_tool_guards_workspace ON workspace_device_tool_guards (workspace_id);
CREATE INDEX workspace_mcp_connections_enabled_idx ON workspace_mcp_connections (workspace_id, name) WHERE enabled;
CREATE UNIQUE INDEX workspace_memberships_personal_account_uq ON workspace_memberships (human_account_id) WHERE is_personal;
CREATE UNIQUE INDEX workspace_memberships_personal_workspace_uq ON workspace_memberships (workspace_id) WHERE is_personal;
CREATE INDEX idx_workspace_model_routes_api_credential ON workspace_model_routes (workspace_id, api_credential_id) WHERE (api_credential_id IS NOT NULL);
CREATE INDEX workspace_model_routes_enabled_idx ON workspace_model_routes (workspace_id, "position") WHERE enabled;
CREATE INDEX workspace_triggers_workspace_created_idx ON workspace_triggers (workspace_id, created_at DESC);

-- 复合外键部分列 ON DELETE SET NULL 等价触发器（对应 PostgreSQL 15+ ON DELETE SET NULL (<col>)）
-- +goose StatementBegin
CREATE TRIGGER trg_card_turns_del_set_null_card_watchers_1
BEFORE DELETE ON card_turns
FOR EACH ROW
BEGIN
    UPDATE card_watchers SET triggered_turn_id = NULL WHERE workspace_id = OLD.workspace_id AND card_id = OLD.card_id AND triggered_turn_id = OLD.id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER trg_card_turns_del_set_null_trigger_deliveries_2
BEFORE DELETE ON card_turns
FOR EACH ROW
BEGIN
    UPDATE trigger_deliveries SET triggered_turn_id = NULL WHERE workspace_id = OLD.workspace_id AND triggered_turn_id = OLD.id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER trg_cards_del_set_null_cards_3
BEFORE DELETE ON cards
FOR EACH ROW
BEGIN
    UPDATE cards SET source_discussion_id = NULL WHERE workspace_id = OLD.workspace_id AND source_discussion_id = OLD.id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER trg_cards_del_set_null_events_4
BEFORE DELETE ON cards
FOR EACH ROW
BEGIN
    UPDATE events SET source_card_id = NULL WHERE workspace_id = OLD.workspace_id AND source_card_id = OLD.id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER trg_cards_del_set_null_remote_channels_5
BEFORE DELETE ON cards
FOR EACH ROW
BEGIN
    UPDATE remote_channels SET target_card_id = NULL WHERE workspace_id = OLD.workspace_id AND target_card_id = OLD.id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER trg_cards_del_set_null_trigger_deliveries_6
BEFORE DELETE ON cards
FOR EACH ROW
BEGIN
    UPDATE trigger_deliveries SET created_card_id = NULL WHERE workspace_id = OLD.workspace_id AND created_card_id = OLD.id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER trg_cards_del_set_null_workspace_attachments_7
BEFORE DELETE ON cards
FOR EACH ROW
BEGIN
    UPDATE workspace_attachments SET card_id = NULL WHERE workspace_id = OLD.workspace_id AND card_id = OLD.id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER trg_events_del_set_null_card_turns_8
BEFORE DELETE ON events
FOR EACH ROW
BEGIN
    UPDATE card_turns SET source_event_id = NULL WHERE workspace_id = OLD.workspace_id AND card_id = OLD.card_id AND source_event_id = OLD.id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER trg_sources_del_set_null_card_tool_approvals_9
BEFORE DELETE ON sources
FOR EACH ROW
BEGIN
    UPDATE card_tool_approvals SET resolved_by_source_id = NULL WHERE workspace_id = OLD.workspace_id AND resolved_by_source_id = OLD.id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER trg_sources_del_set_null_card_tool_whitelists_10
BEFORE DELETE ON sources
FOR EACH ROW
BEGIN
    UPDATE card_tool_whitelists SET created_by_source_id = NULL WHERE workspace_id = OLD.workspace_id AND created_by_source_id = OLD.id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER trg_sources_del_set_null_card_watchers_11
BEFORE DELETE ON sources
FOR EACH ROW
BEGIN
    UPDATE card_watchers SET created_by_source_id = NULL WHERE workspace_id = OLD.workspace_id AND created_by_source_id = OLD.id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER trg_sources_del_set_null_remote_channels_12
BEFORE DELETE ON sources
FOR EACH ROW
BEGIN
    UPDATE remote_channels SET created_by_source_id = NULL WHERE workspace_id = OLD.workspace_id AND created_by_source_id = OLD.id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER trg_sources_del_set_null_workspace_triggers_13
BEFORE DELETE ON sources
FOR EACH ROW
BEGIN
    UPDATE workspace_triggers SET created_by_source_id = NULL WHERE workspace_id = OLD.workspace_id AND created_by_source_id = OLD.id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER trg_turn_attempts_del_set_null_card_watchers_14
BEFORE DELETE ON turn_attempts
FOR EACH ROW
BEGIN
    UPDATE card_watchers SET created_by_attempt_id = NULL WHERE workspace_id = OLD.workspace_id AND card_id = OLD.card_id AND created_by_attempt_id = OLD.id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER trg_turn_attempts_del_set_null_events_15
BEFORE DELETE ON turn_attempts
FOR EACH ROW
BEGIN
    UPDATE events SET attempt_id = NULL WHERE workspace_id = OLD.workspace_id AND card_id = OLD.card_id AND attempt_id = OLD.id;
END;
-- +goose StatementEnd

-- +goose Down
PRAGMA foreign_keys = OFF;
DROP TABLE IF EXISTS workspaces;
DROP TABLE IF EXISTS workspace_triggers;
DROP TABLE IF EXISTS workspace_nexusdock_connections;
DROP TABLE IF EXISTS workspace_model_routes;
DROP TABLE IF EXISTS workspace_memberships;
DROP TABLE IF EXISTS workspace_mcp_connections;
DROP TABLE IF EXISTS workspace_device_tool_guards;
DROP TABLE IF EXISTS workspace_attachments;
DROP TABLE IF EXISTS workspace_api_credentials;
DROP TABLE IF EXISTS workspace_agent_settings;
DROP TABLE IF EXISTS web_sessions;
DROP TABLE IF EXISTS web_auth_flows;
DROP TABLE IF EXISTS turn_attempts;
DROP TABLE IF EXISTS trigger_deliveries;
DROP TABLE IF EXISTS sources;
DROP TABLE IF EXISTS remote_channels;
DROP TABLE IF EXISTS remote_channel_outbound_cursors;
DROP TABLE IF EXISTS remote_channel_inbound_dedup;
DROP TABLE IF EXISTS push_subscriptions;
DROP TABLE IF EXISTS push_subscription_card_revisions;
DROP TABLE IF EXISTS push_card_revisions;
DROP TABLE IF EXISTS projects;
DROP TABLE IF EXISTS oidc_identities;
DROP TABLE IF EXISTS oauth_tokens;
DROP TABLE IF EXISTS oauth_grants;
DROP TABLE IF EXISTS oauth_clients;
DROP TABLE IF EXISTS oauth_authorization_codes;
DROP TABLE IF EXISTS nexusdock_oauth_clients;
DROP TABLE IF EXISTS nexusdock_auth_flows;
DROP TABLE IF EXISTS model_oauth_credentials;
DROP TABLE IF EXISTS model_auth_flows;
DROP TABLE IF EXISTS human_accounts;
DROP TABLE IF EXISTS events;
DROP TABLE IF EXISTS cards;
DROP TABLE IF EXISTS card_watchers;
DROP TABLE IF EXISTS card_turns;
DROP TABLE IF EXISTS card_tool_whitelists;
DROP TABLE IF EXISTS card_tool_approvals;
DROP TABLE IF EXISTS card_schedules;
DROP TABLE IF EXISTS card_read_cursors;
DROP TABLE IF EXISTS card_attachments;
DROP TABLE IF EXISTS agent_user_state;
DROP TABLE IF EXISTS agent_sessions;
DROP TABLE IF EXISTS agent_session_events;
DROP TABLE IF EXISTS agent_model_calls;
DROP TABLE IF EXISTS agent_artifacts;
DROP TABLE IF EXISTS agent_app_state;
PRAGMA foreign_keys = ON;
