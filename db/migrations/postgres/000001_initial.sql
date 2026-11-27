-- +goose Up








CREATE TABLE agent_app_state (
    app_name text NOT NULL,
    state jsonb DEFAULT '{}'::jsonb NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT agent_app_state_app_name_check CHECK ((btrim(app_name) <> ''::text)),
    CONSTRAINT agent_app_state_state_check CHECK ((jsonb_typeof(state) = 'object'::text))
);



CREATE TABLE agent_artifacts (
    app_name text NOT NULL,
    user_id text NOT NULL,
    session_id text NOT NULL,
    file_name text NOT NULL,
    version bigint NOT NULL,
    part jsonb NOT NULL,
    mime_type text DEFAULT ''::text NOT NULL,
    custom_metadata jsonb DEFAULT '{}'::jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT agent_artifacts_app_name_check CHECK ((btrim(app_name) <> ''::text)),
    CONSTRAINT agent_artifacts_custom_metadata_check CHECK ((jsonb_typeof(custom_metadata) = 'object'::text)),
    CONSTRAINT agent_artifacts_file_name_check CHECK ((btrim(file_name) <> ''::text)),
    CONSTRAINT agent_artifacts_part_check CHECK ((jsonb_typeof(part) = 'object'::text)),
    CONSTRAINT agent_artifacts_session_id_check CHECK ((btrim(session_id) <> ''::text)),
    CONSTRAINT agent_artifacts_user_id_check CHECK ((btrim(user_id) <> ''::text)),
    CONSTRAINT agent_artifacts_version_check CHECK ((version > 0))
);



CREATE TABLE agent_session_events (
    sequence bigint NOT NULL,
    app_name text NOT NULL,
    user_id text NOT NULL,
    session_id text NOT NULL,
    id text NOT NULL,
    invocation_id text DEFAULT ''::text NOT NULL,
    occurred_at timestamp with time zone NOT NULL,
    event jsonb NOT NULL,
    CONSTRAINT agent_session_events_event_check CHECK ((jsonb_typeof(event) = 'object'::text)),
    CONSTRAINT agent_session_events_id_check CHECK ((btrim(id) <> ''::text))
);



ALTER TABLE agent_session_events ALTER COLUMN sequence ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME agent_session_events_sequence_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);



CREATE TABLE agent_sessions (
    app_name text NOT NULL,
    user_id text NOT NULL,
    id text NOT NULL,
    state jsonb DEFAULT '{}'::jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT agent_sessions_app_name_check CHECK ((btrim(app_name) <> ''::text)),
    CONSTRAINT agent_sessions_id_check CHECK ((btrim(id) <> ''::text)),
    CONSTRAINT agent_sessions_state_check CHECK ((jsonb_typeof(state) = 'object'::text)),
    CONSTRAINT agent_sessions_user_id_check CHECK ((btrim(user_id) <> ''::text))
);



CREATE TABLE agent_user_state (
    app_name text NOT NULL,
    user_id text NOT NULL,
    state jsonb DEFAULT '{}'::jsonb NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT agent_user_state_app_name_check CHECK ((btrim(app_name) <> ''::text)),
    CONSTRAINT agent_user_state_state_check CHECK ((jsonb_typeof(state) = 'object'::text)),
    CONSTRAINT agent_user_state_user_id_check CHECK ((btrim(user_id) <> ''::text))
);



CREATE TABLE card_schedules (
    id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    card_id uuid NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    frequency text NOT NULL,
    day_of_week smallint,
    day_of_month smallint,
    time_of_day time without time zone NOT NULL,
    timezone text NOT NULL,
    next_trigger_at timestamp with time zone NOT NULL,
    last_triggered_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT card_schedules_day_of_month_check CHECK (((day_of_month >= 1) AND (day_of_month <= 28))),
    CONSTRAINT card_schedules_day_of_week_check CHECK (((day_of_week >= 0) AND (day_of_week <= 6))),
    CONSTRAINT card_schedules_frequency_check CHECK ((frequency = ANY (ARRAY['daily'::text, 'weekly'::text, 'monthly'::text]))),
    CONSTRAINT card_schedules_shape_ck CHECK ((((frequency = 'daily'::text) AND (day_of_week IS NULL) AND (day_of_month IS NULL)) OR ((frequency = 'weekly'::text) AND (day_of_week IS NOT NULL) AND (day_of_month IS NULL)) OR ((frequency = 'monthly'::text) AND (day_of_week IS NULL) AND (day_of_month IS NOT NULL)))),
    CONSTRAINT card_schedules_timezone_check CHECK ((btrim(timezone) <> ''::text))
);



CREATE TABLE card_turns (
    id uuid NOT NULL,
    sequence bigint NOT NULL,
    workspace_id uuid NOT NULL,
    card_id uuid NOT NULL,
    source text NOT NULL,
    content text NOT NULL,
    created_by_source_id uuid,
    source_event_id uuid,
    dispatch_mode text NOT NULL,
    schedule_id uuid,
    scheduled_for timestamp with time zone,
    idempotency_key text,
    request_hash text,
    cancelled_at timestamp with time zone,
    cancelled_by_source_id uuid,
    cancel_reason text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT card_turns_cancel_shape_ck CHECK ((((cancelled_at IS NULL) AND (cancelled_by_source_id IS NULL) AND (cancel_reason = ''::text)) OR ((cancelled_at IS NOT NULL) AND (cancelled_by_source_id IS NOT NULL)))),
    CONSTRAINT card_turns_content_check CHECK ((btrim(content) <> ''::text)),
    CONSTRAINT card_turns_dispatch_mode_check CHECK ((dispatch_mode = ANY (ARRAY['enqueue'::text, 'interrupt'::text]))),
    CONSTRAINT card_turns_source_check CHECK ((source = ANY (ARRAY['human'::text, 'schedule'::text, 'system'::text]))),
    CONSTRAINT card_turns_source_shape_ck CHECK ((((source = 'human'::text) AND (created_by_source_id IS NOT NULL) AND (schedule_id IS NULL) AND (scheduled_for IS NULL)) OR ((source = 'schedule'::text) AND (created_by_source_id IS NULL) AND (schedule_id IS NOT NULL) AND (scheduled_for IS NOT NULL) AND (dispatch_mode = 'enqueue'::text)) OR ((source = 'system'::text) AND (created_by_source_id IS NOT NULL) AND (schedule_id IS NULL) AND (scheduled_for IS NULL) AND (dispatch_mode = 'enqueue'::text))))
);



ALTER TABLE card_turns ALTER COLUMN sequence ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME card_turns_sequence_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);



CREATE TABLE cards (
    id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    project_id uuid NOT NULL,
    kind text NOT NULL,
    title text DEFAULT ''::text NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    discussion_state text,
    todo_state text,
    ongoing_state text,
    source_discussion_id uuid,
    required_confirmations integer DEFAULT 1 NOT NULL,
    created_by_source_id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    create_idempotency_key text,
    create_request_hash text,
    CONSTRAINT cards_create_request_identity_ck CHECK ((((create_idempotency_key IS NULL) AND (create_request_hash IS NULL)) OR ((create_idempotency_key IS NOT NULL) AND (btrim(create_idempotency_key) <> ''::text) AND (create_request_hash IS NOT NULL)))),
    CONSTRAINT cards_discussion_state_check CHECK ((discussion_state = ANY (ARRAY['active'::text, 'archived'::text]))),
    CONSTRAINT cards_kind_check CHECK ((kind = ANY (ARRAY['discussion'::text, 'todo'::text, 'ongoing'::text]))),
    CONSTRAINT cards_kind_state_ck CHECK ((((kind = 'discussion'::text) AND (discussion_state IS NOT NULL) AND (todo_state IS NULL) AND (ongoing_state IS NULL) AND (source_discussion_id IS NULL)) OR ((kind = 'todo'::text) AND (discussion_state IS NULL) AND (todo_state IS NOT NULL) AND (ongoing_state IS NULL)) OR ((kind = 'ongoing'::text) AND (discussion_state IS NULL) AND (todo_state IS NULL) AND (ongoing_state IS NOT NULL)))),
    CONSTRAINT cards_ongoing_state_check CHECK ((ongoing_state = ANY (ARRAY['active'::text, 'paused'::text, 'archived'::text]))),
    CONSTRAINT cards_required_confirmations_check CHECK ((required_confirmations > 0)),
    CONSTRAINT cards_title_ck CHECK (((kind = 'discussion'::text) OR (btrim(title) <> ''::text))),
    CONSTRAINT cards_todo_state_check CHECK ((todo_state = ANY (ARRAY['todo'::text, 'working'::text, 'review'::text, 'done'::text])))
);



CREATE TABLE events (
    id uuid NOT NULL,
    sequence bigint NOT NULL,
    workspace_id uuid NOT NULL,
    card_id uuid NOT NULL,
    event_type text NOT NULL,
    actor_source_id uuid NOT NULL,
    attempt_id uuid,
    content text DEFAULT ''::text NOT NULL,
    checkin_status text,
    idempotency_key text,
    request_hash text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT events_checkin_ck CHECK ((((event_type = 'checkin'::text) AND (checkin_status IS NOT NULL)) OR ((event_type <> 'checkin'::text) AND (checkin_status IS NULL)))),
    CONSTRAINT events_checkin_status_check CHECK ((checkin_status = ANY (ARRAY['healthy'::text, 'warning'::text, 'critical'::text]))),
    CONSTRAINT events_event_type_check CHECK ((event_type = ANY (ARRAY['comment'::text, 'progress'::text, 'finding'::text, 'result'::text, 'checkin'::text, 'confirm'::text, 'reject'::text, 'status_change'::text])))
);



ALTER TABLE events ALTER COLUMN sequence ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME events_sequence_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);



CREATE TABLE human_accounts (
    id uuid NOT NULL,
    display_name text NOT NULL,
    email_snapshot text,
    is_local_admin boolean DEFAULT false NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT human_accounts_display_name_check CHECK ((btrim(display_name) <> ''::text))
);



CREATE TABLE model_auth_flows (
    id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    provider text NOT NULL,
    state_digest bytea,
    payload_ciphertext bytea NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT model_auth_flows_payload_ciphertext_check CHECK ((octet_length(payload_ciphertext) > 28)),
    CONSTRAINT model_auth_flows_provider_check CHECK ((provider = ANY (ARRAY['chatgpt'::text, 'grok'::text]))),
    CONSTRAINT model_auth_flows_state_digest_check CHECK (((state_digest IS NULL) OR (octet_length(state_digest) = 32)))
);



CREATE TABLE model_oauth_credentials (
    workspace_id uuid NOT NULL,
    provider text NOT NULL,
    credential_ciphertext bytea NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    display_label text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    id uuid NOT NULL,
    account_subject text DEFAULT ''::text NOT NULL,
    CONSTRAINT model_oauth_credentials_credential_ciphertext_check CHECK ((octet_length(credential_ciphertext) > 28)),
    CONSTRAINT model_oauth_credentials_provider_check CHECK ((provider = ANY (ARRAY['chatgpt'::text, 'grok'::text])))
);



CREATE TABLE nexusdock_auth_flows (
    state_digest bytea NOT NULL,
    workspace_id uuid NOT NULL,
    human_account_id uuid NOT NULL,
    source_id uuid NOT NULL,
    client_id text NOT NULL,
    code_verifier text NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    consumed_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);



CREATE TABLE nexusdock_oauth_clients (
    resource text NOT NULL,
    client_id text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);



CREATE TABLE oauth_authorization_codes (
    code_digest bytea NOT NULL,
    grant_id uuid NOT NULL,
    redirect_uri text NOT NULL,
    code_challenge text NOT NULL,
    scope text NOT NULL,
    resource text NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    consumed_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT oauth_authorization_codes_check CHECK ((expires_at > created_at)),
    CONSTRAINT oauth_authorization_codes_code_challenge_check CHECK (((length(code_challenge) >= 43) AND (length(code_challenge) <= 128))),
    CONSTRAINT oauth_authorization_codes_code_digest_check CHECK ((octet_length(code_digest) = 32)),
    CONSTRAINT oauth_authorization_codes_redirect_uri_check CHECK ((btrim(redirect_uri) <> ''::text)),
    CONSTRAINT oauth_authorization_codes_resource_check CHECK ((btrim(resource) <> ''::text)),
    CONSTRAINT oauth_authorization_codes_scope_check CHECK ((btrim(scope) <> ''::text))
);



CREATE TABLE oauth_clients (
    client_id text NOT NULL,
    client_name text NOT NULL,
    redirect_uris text[] NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT oauth_clients_client_id_check CHECK ((btrim(client_id) <> ''::text)),
    CONSTRAINT oauth_clients_client_name_check CHECK ((btrim(client_name) <> ''::text)),
    CONSTRAINT oauth_clients_redirect_uris_check CHECK (((cardinality(redirect_uris) >= 1) AND (cardinality(redirect_uris) <= 20)))
);



CREATE TABLE oauth_grants (
    id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    human_account_id uuid NOT NULL,
    human_source_id uuid NOT NULL,
    client_id text NOT NULL,
    source_id uuid NOT NULL,
    resource text NOT NULL,
    scope text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    revoked_at timestamp with time zone,
    CONSTRAINT oauth_grants_distinct_sources_ck CHECK ((human_source_id <> source_id)),
    CONSTRAINT oauth_grants_resource_check CHECK ((btrim(resource) <> ''::text)),
    CONSTRAINT oauth_grants_scope_check CHECK ((btrim(scope) <> ''::text))
);



CREATE TABLE oauth_tokens (
    id uuid NOT NULL,
    grant_id uuid NOT NULL,
    family_id uuid NOT NULL,
    access_token_digest bytea NOT NULL,
    refresh_token_digest bytea NOT NULL,
    access_expires_at timestamp with time zone NOT NULL,
    refresh_expires_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    revoked_at timestamp with time zone,
    CONSTRAINT oauth_tokens_access_token_digest_check CHECK ((octet_length(access_token_digest) = 32)),
    CONSTRAINT oauth_tokens_check CHECK ((access_expires_at > created_at)),
    CONSTRAINT oauth_tokens_check1 CHECK ((refresh_expires_at > access_expires_at)),
    CONSTRAINT oauth_tokens_refresh_token_digest_check CHECK ((octet_length(refresh_token_digest) = 32))
);



CREATE TABLE oidc_identities (
    issuer text NOT NULL,
    subject text NOT NULL,
    human_account_id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT oidc_identities_issuer_check CHECK ((btrim(issuer) <> ''::text)),
    CONSTRAINT oidc_identities_subject_check CHECK ((btrim(subject) <> ''::text))
);



CREATE TABLE projects (
    id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    name text NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    instructions text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT projects_name_check CHECK ((btrim(name) <> ''::text))
);



CREATE TABLE sources (
    id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    kind text NOT NULL,
    name text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT sources_kind_check CHECK ((kind = ANY (ARRAY['human'::text, 'cycle_agent'::text, 'oauth_client'::text]))),
    CONSTRAINT sources_name_check CHECK ((btrim(name) <> ''::text))
);



CREATE TABLE turn_attempts (
    id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    card_id uuid NOT NULL,
    turn_id uuid NOT NULL,
    attempt_number integer NOT NULL,
    reason text NOT NULL,
    retry_of_attempt_id uuid,
    runner_source_id uuid NOT NULL,
    invocation_id text,
    status text NOT NULL,
    current_stage text DEFAULT ''::text NOT NULL,
    progress_summary text DEFAULT ''::text NOT NULL,
    result_summary text DEFAULT ''::text NOT NULL,
    error_summary text DEFAULT ''::text NOT NULL,
    started_at timestamp with time zone,
    heartbeat_at timestamp with time zone,
    lease_expires_at timestamp with time zone,
    finished_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT turn_attempts_attempt_number_check CHECK ((attempt_number > 0)),
    CONSTRAINT turn_attempts_invocation_id_check CHECK (((invocation_id IS NULL) OR (btrim(invocation_id) <> ''::text))),
    CONSTRAINT turn_attempts_lifecycle_ck CHECK ((((status = 'queued'::text) AND (started_at IS NULL) AND (heartbeat_at IS NULL) AND (lease_expires_at IS NULL) AND (finished_at IS NULL)) OR ((status = 'running'::text) AND (started_at IS NOT NULL) AND (heartbeat_at IS NOT NULL) AND (lease_expires_at IS NOT NULL) AND (finished_at IS NULL)) OR ((status = 'waiting_human'::text) AND (started_at IS NOT NULL) AND (heartbeat_at IS NULL) AND (lease_expires_at IS NULL) AND (finished_at IS NULL)) OR ((status = ANY (ARRAY['completed'::text, 'failed'::text, 'superseded'::text, 'expired'::text, 'cancelled'::text])) AND (finished_at IS NOT NULL) AND (heartbeat_at IS NULL) AND (lease_expires_at IS NULL)))),
    CONSTRAINT turn_attempts_reason_check CHECK ((reason = ANY (ARRAY['initial'::text, 'retry'::text, 'recovery'::text]))),
    CONSTRAINT turn_attempts_reason_shape_ck CHECK ((((reason = 'initial'::text) AND (retry_of_attempt_id IS NULL) AND (attempt_number = 1)) OR ((reason = ANY (ARRAY['retry'::text, 'recovery'::text])) AND (retry_of_attempt_id IS NOT NULL) AND (attempt_number > 1)))),
    CONSTRAINT turn_attempts_status_check CHECK ((status = ANY (ARRAY['queued'::text, 'running'::text, 'waiting_human'::text, 'completed'::text, 'failed'::text, 'superseded'::text, 'expired'::text, 'cancelled'::text])))
);



CREATE TABLE web_auth_flows (
    state_digest bytea NOT NULL,
    provider text NOT NULL,
    code_verifier text NOT NULL,
    nonce text NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    consumed_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT web_auth_flows_code_verifier_check CHECK ((btrim(code_verifier) <> ''::text)),
    CONSTRAINT web_auth_flows_nonce_check CHECK ((btrim(nonce) <> ''::text)),
    CONSTRAINT web_auth_flows_provider_check CHECK ((provider = 'oidc'::text)),
    CONSTRAINT web_auth_flows_state_digest_check CHECK ((octet_length(state_digest) = 32))
);



CREATE TABLE web_sessions (
    id uuid NOT NULL,
    human_account_id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    source_id uuid NOT NULL,
    token_digest bytea NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT web_sessions_token_digest_check CHECK ((octet_length(token_digest) = 32))
);



CREATE TABLE workspace_agent_settings (
    workspace_id uuid NOT NULL,
    source_id uuid NOT NULL,
    system_instructions text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);



CREATE TABLE workspace_mcp_connections (
    id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    name text NOT NULL,
    url text NOT NULL,
    bearer_token_env text DEFAULT ''::text NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    timeout_ms integer DEFAULT 30000 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT workspace_mcp_connections_name_check CHECK ((btrim(name) <> ''::text)),
    CONSTRAINT workspace_mcp_connections_timeout_ms_check CHECK (((timeout_ms >= 1000) AND (timeout_ms <= 300000))),
    CONSTRAINT workspace_mcp_connections_url_check CHECK ((btrim(url) <> ''::text))
);



CREATE TABLE workspace_memberships (
    workspace_id uuid NOT NULL,
    human_account_id uuid NOT NULL,
    source_id uuid NOT NULL,
    role text NOT NULL,
    is_personal boolean DEFAULT false NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT workspace_memberships_role_check CHECK ((role = ANY (ARRAY['owner'::text, 'member'::text])))
);



CREATE TABLE workspace_model_routes (
    id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    "position" integer NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    provider_type text NOT NULL,
    credential_id uuid,
    provider_base_url text DEFAULT ''::text NOT NULL,
    model text NOT NULL,
    api_key_env text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT workspace_model_routes_model_check CHECK ((btrim(model) <> ''::text)),
    CONSTRAINT workspace_model_routes_position_check CHECK (("position" >= 0)),
    CONSTRAINT workspace_model_routes_provider_ck CHECK ((((provider_type = 'openai_compatible'::text) AND (credential_id IS NULL) AND (btrim(provider_base_url) <> ''::text)) OR ((provider_type = ANY (ARRAY['chatgpt'::text, 'grok'::text])) AND (btrim(provider_base_url) = ''::text) AND (btrim(api_key_env) = ''::text) AND ((NOT enabled) OR (credential_id IS NOT NULL))))),
    CONSTRAINT workspace_model_routes_provider_type_check CHECK ((provider_type = ANY (ARRAY['openai_compatible'::text, 'chatgpt'::text, 'grok'::text])))
);



CREATE TABLE workspace_nexusdock_connections (
    workspace_id uuid NOT NULL,
    resource text NOT NULL,
    client_id text NOT NULL,
    access_token_ciphertext bytea NOT NULL,
    refresh_token_ciphertext bytea NOT NULL,
    access_expires_at timestamp with time zone NOT NULL,
    connected_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);



CREATE TABLE workspaces (
    id uuid NOT NULL,
    name text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT workspaces_name_check CHECK ((btrim(name) <> ''::text))
);



ALTER TABLE ONLY agent_app_state
    ADD CONSTRAINT agent_app_state_pkey PRIMARY KEY (app_name);



ALTER TABLE ONLY agent_artifacts
    ADD CONSTRAINT agent_artifacts_pkey PRIMARY KEY (app_name, user_id, session_id, file_name, version);



ALTER TABLE ONLY agent_session_events
    ADD CONSTRAINT agent_session_events_pkey PRIMARY KEY (app_name, user_id, session_id, id);



ALTER TABLE ONLY agent_sessions
    ADD CONSTRAINT agent_sessions_pkey PRIMARY KEY (app_name, user_id, id);



ALTER TABLE ONLY agent_user_state
    ADD CONSTRAINT agent_user_state_pkey PRIMARY KEY (app_name, user_id);



ALTER TABLE ONLY card_schedules
    ADD CONSTRAINT card_schedules_pkey PRIMARY KEY (id);



ALTER TABLE ONLY card_schedules
    ADD CONSTRAINT card_schedules_workspace_id_card_id_id_key UNIQUE (workspace_id, card_id, id);



ALTER TABLE ONLY card_schedules
    ADD CONSTRAINT card_schedules_workspace_id_id_key UNIQUE (workspace_id, id);



ALTER TABLE ONLY card_turns
    ADD CONSTRAINT card_turns_pkey PRIMARY KEY (id);



ALTER TABLE ONLY card_turns
    ADD CONSTRAINT card_turns_sequence_key UNIQUE (sequence);



ALTER TABLE ONLY card_turns
    ADD CONSTRAINT card_turns_workspace_id_card_id_id_key UNIQUE (workspace_id, card_id, id);



ALTER TABLE ONLY card_turns
    ADD CONSTRAINT card_turns_workspace_id_id_key UNIQUE (workspace_id, id);



ALTER TABLE ONLY cards
    ADD CONSTRAINT cards_pkey PRIMARY KEY (id);



ALTER TABLE ONLY cards
    ADD CONSTRAINT cards_workspace_id_id_key UNIQUE (workspace_id, id);



ALTER TABLE ONLY events
    ADD CONSTRAINT events_pkey PRIMARY KEY (id);



ALTER TABLE ONLY events
    ADD CONSTRAINT events_sequence_key UNIQUE (sequence);



ALTER TABLE ONLY events
    ADD CONSTRAINT events_workspace_id_card_id_id_key UNIQUE (workspace_id, card_id, id);



ALTER TABLE ONLY events
    ADD CONSTRAINT events_workspace_id_id_key UNIQUE (workspace_id, id);



ALTER TABLE ONLY human_accounts
    ADD CONSTRAINT human_accounts_pkey PRIMARY KEY (id);



ALTER TABLE ONLY model_auth_flows
    ADD CONSTRAINT model_auth_flows_pkey PRIMARY KEY (id);



ALTER TABLE ONLY model_oauth_credentials
    ADD CONSTRAINT model_oauth_credentials_pkey PRIMARY KEY (id);



ALTER TABLE ONLY model_oauth_credentials
    ADD CONSTRAINT model_oauth_credentials_workspace_id_id_key UNIQUE (workspace_id, id);



ALTER TABLE ONLY nexusdock_auth_flows
    ADD CONSTRAINT nexusdock_auth_flows_pkey PRIMARY KEY (state_digest);



ALTER TABLE ONLY nexusdock_oauth_clients
    ADD CONSTRAINT nexusdock_oauth_clients_client_id_key UNIQUE (client_id);



ALTER TABLE ONLY nexusdock_oauth_clients
    ADD CONSTRAINT nexusdock_oauth_clients_pkey PRIMARY KEY (resource);



ALTER TABLE ONLY oauth_authorization_codes
    ADD CONSTRAINT oauth_authorization_codes_pkey PRIMARY KEY (code_digest);



ALTER TABLE ONLY oauth_clients
    ADD CONSTRAINT oauth_clients_pkey PRIMARY KEY (client_id);



ALTER TABLE ONLY oauth_grants
    ADD CONSTRAINT oauth_grants_pkey PRIMARY KEY (id);



ALTER TABLE ONLY oauth_grants
    ADD CONSTRAINT oauth_grants_workspace_id_client_id_resource_key UNIQUE (workspace_id, client_id, resource);



ALTER TABLE ONLY oauth_grants
    ADD CONSTRAINT oauth_grants_workspace_id_source_id_key UNIQUE (workspace_id, source_id);



ALTER TABLE ONLY oauth_tokens
    ADD CONSTRAINT oauth_tokens_access_token_digest_key UNIQUE (access_token_digest);



ALTER TABLE ONLY oauth_tokens
    ADD CONSTRAINT oauth_tokens_pkey PRIMARY KEY (id);



ALTER TABLE ONLY oauth_tokens
    ADD CONSTRAINT oauth_tokens_refresh_token_digest_key UNIQUE (refresh_token_digest);



ALTER TABLE ONLY oidc_identities
    ADD CONSTRAINT oidc_identities_pkey PRIMARY KEY (issuer, subject);



ALTER TABLE ONLY projects
    ADD CONSTRAINT projects_pkey PRIMARY KEY (id);



ALTER TABLE ONLY projects
    ADD CONSTRAINT projects_workspace_id_id_key UNIQUE (workspace_id, id);



ALTER TABLE ONLY sources
    ADD CONSTRAINT sources_pkey PRIMARY KEY (id);



ALTER TABLE ONLY sources
    ADD CONSTRAINT sources_workspace_id_id_key UNIQUE (workspace_id, id);



ALTER TABLE ONLY turn_attempts
    ADD CONSTRAINT turn_attempts_pkey PRIMARY KEY (id);



ALTER TABLE ONLY turn_attempts
    ADD CONSTRAINT turn_attempts_workspace_id_card_id_id_key UNIQUE (workspace_id, card_id, id);



ALTER TABLE ONLY turn_attempts
    ADD CONSTRAINT turn_attempts_workspace_id_id_key UNIQUE (workspace_id, id);



ALTER TABLE ONLY turn_attempts
    ADD CONSTRAINT turn_attempts_workspace_id_turn_id_attempt_number_key UNIQUE (workspace_id, turn_id, attempt_number);



ALTER TABLE ONLY web_auth_flows
    ADD CONSTRAINT web_auth_flows_pkey PRIMARY KEY (state_digest);



ALTER TABLE ONLY web_sessions
    ADD CONSTRAINT web_sessions_pkey PRIMARY KEY (id);



ALTER TABLE ONLY web_sessions
    ADD CONSTRAINT web_sessions_token_digest_key UNIQUE (token_digest);



ALTER TABLE ONLY workspace_agent_settings
    ADD CONSTRAINT workspace_agent_settings_pkey PRIMARY KEY (workspace_id);



ALTER TABLE ONLY workspace_agent_settings
    ADD CONSTRAINT workspace_agent_settings_source_id_key UNIQUE (source_id);



ALTER TABLE ONLY workspace_agent_settings
    ADD CONSTRAINT workspace_agent_settings_workspace_id_source_id_key UNIQUE (workspace_id, source_id);



ALTER TABLE ONLY workspace_mcp_connections
    ADD CONSTRAINT workspace_mcp_connections_pkey PRIMARY KEY (id);



ALTER TABLE ONLY workspace_mcp_connections
    ADD CONSTRAINT workspace_mcp_connections_workspace_id_id_key UNIQUE (workspace_id, id);



ALTER TABLE ONLY workspace_mcp_connections
    ADD CONSTRAINT workspace_mcp_connections_workspace_id_name_key UNIQUE (workspace_id, name);



ALTER TABLE ONLY workspace_memberships
    ADD CONSTRAINT workspace_memberships_pkey PRIMARY KEY (workspace_id, human_account_id);



ALTER TABLE ONLY workspace_memberships
    ADD CONSTRAINT workspace_memberships_source_id_key UNIQUE (source_id);



ALTER TABLE ONLY workspace_memberships
    ADD CONSTRAINT workspace_memberships_workspace_id_human_account_id_source__key UNIQUE (workspace_id, human_account_id, source_id);



ALTER TABLE ONLY workspace_model_routes
    ADD CONSTRAINT workspace_model_routes_pkey PRIMARY KEY (id);



ALTER TABLE ONLY workspace_model_routes
    ADD CONSTRAINT workspace_model_routes_workspace_id_id_key UNIQUE (workspace_id, id);



ALTER TABLE ONLY workspace_model_routes
    ADD CONSTRAINT workspace_model_routes_workspace_id_position_key UNIQUE (workspace_id, "position");



ALTER TABLE ONLY workspace_nexusdock_connections
    ADD CONSTRAINT workspace_nexusdock_connections_pkey PRIMARY KEY (workspace_id);



ALTER TABLE ONLY workspaces
    ADD CONSTRAINT workspaces_pkey PRIMARY KEY (id);



CREATE INDEX agent_artifacts_list_idx ON agent_artifacts USING btree (app_name, user_id, session_id, file_name, version DESC);



CREATE INDEX agent_session_events_history_idx ON agent_session_events USING btree (app_name, user_id, session_id, occurred_at, id);



CREATE INDEX agent_sessions_user_updated_idx ON agent_sessions USING btree (app_name, user_id, updated_at DESC);



CREATE INDEX card_schedules_due_idx ON card_schedules USING btree (next_trigger_at) WHERE enabled;



CREATE INDEX card_turns_pending_idx ON card_turns USING btree (workspace_id, sequence) WHERE (cancelled_at IS NULL);



CREATE UNIQUE INDEX card_turns_schedule_occurrence_uq ON card_turns USING btree (schedule_id, scheduled_for) WHERE (schedule_id IS NOT NULL);



CREATE UNIQUE INDEX card_turns_source_idempotency_uq ON card_turns USING btree (workspace_id, created_by_source_id, idempotency_key) WHERE ((created_by_source_id IS NOT NULL) AND (idempotency_key IS NOT NULL));



CREATE INDEX cards_project_idx ON cards USING btree (workspace_id, project_id, created_at);



CREATE UNIQUE INDEX cards_source_create_idempotency_uq ON cards USING btree (workspace_id, created_by_source_id, create_idempotency_key) WHERE (create_idempotency_key IS NOT NULL);



CREATE INDEX events_card_sequence_idx ON events USING btree (workspace_id, card_id, sequence);



CREATE INDEX events_attempt_idx ON events USING btree (workspace_id, card_id, attempt_id, sequence) WHERE (attempt_id IS NOT NULL);



CREATE UNIQUE INDEX events_comment_idempotency_uq ON events USING btree (workspace_id, actor_source_id, idempotency_key) WHERE ((event_type = 'comment'::text) AND (idempotency_key IS NOT NULL));



CREATE UNIQUE INDEX events_confirm_source_uq ON events USING btree (workspace_id, card_id, actor_source_id) WHERE (event_type = 'confirm'::text);



CREATE UNIQUE INDEX events_source_type_idempotency_uq ON events USING btree (workspace_id, actor_source_id, event_type, idempotency_key) WHERE ((idempotency_key IS NOT NULL) AND (event_type = ANY (ARRAY['progress'::text, 'finding'::text, 'checkin'::text, 'confirm'::text])));



CREATE UNIQUE INDEX human_accounts_single_local_admin_uq ON human_accounts USING btree (is_local_admin) WHERE is_local_admin;



CREATE UNIQUE INDEX model_auth_flows_state_idx ON model_auth_flows USING btree (state_digest) WHERE (state_digest IS NOT NULL);



CREATE INDEX model_auth_flows_workspace_idx ON model_auth_flows USING btree (workspace_id, provider, expires_at);



CREATE UNIQUE INDEX model_oauth_credentials_account_identity_idx ON model_oauth_credentials USING btree (workspace_id, provider, account_subject) WHERE (btrim(account_subject) <> ''::text);



CREATE INDEX nexusdock_auth_flows_expiry_idx ON nexusdock_auth_flows USING btree (expires_at);



CREATE INDEX oauth_authorization_codes_expiry_idx ON oauth_authorization_codes USING btree (expires_at);



CREATE INDEX oauth_grants_workspace_client_idx ON oauth_grants USING btree (workspace_id, client_id);



CREATE INDEX oauth_tokens_access_expiry_idx ON oauth_tokens USING btree (access_expires_at);



CREATE INDEX oauth_tokens_family_idx ON oauth_tokens USING btree (family_id);



CREATE INDEX oauth_tokens_refresh_expiry_idx ON oauth_tokens USING btree (refresh_expires_at);



CREATE INDEX oidc_identities_human_account_idx ON oidc_identities USING btree (human_account_id);



CREATE INDEX projects_workspace_idx ON projects USING btree (workspace_id, created_at);



CREATE UNIQUE INDEX turn_attempts_invocation_uq ON turn_attempts USING btree (workspace_id, invocation_id) WHERE (invocation_id IS NOT NULL);



CREATE UNIQUE INDEX turn_attempts_one_current_per_card_uq ON turn_attempts USING btree (workspace_id, card_id) WHERE (status = ANY (ARRAY['queued'::text, 'running'::text, 'waiting_human'::text]));



CREATE UNIQUE INDEX turn_attempts_one_successor_uq ON turn_attempts USING btree (retry_of_attempt_id) WHERE (retry_of_attempt_id IS NOT NULL);



CREATE INDEX turn_attempts_turn_history_idx ON turn_attempts USING btree (workspace_id, turn_id, attempt_number);



CREATE INDEX web_auth_flows_expiry_idx ON web_auth_flows USING btree (expires_at);



CREATE INDEX web_sessions_expiry_idx ON web_sessions USING btree (expires_at);



CREATE INDEX workspace_mcp_connections_enabled_idx ON workspace_mcp_connections USING btree (workspace_id, name) WHERE enabled;



CREATE UNIQUE INDEX workspace_memberships_personal_account_uq ON workspace_memberships USING btree (human_account_id) WHERE is_personal;



CREATE UNIQUE INDEX workspace_memberships_personal_workspace_uq ON workspace_memberships USING btree (workspace_id) WHERE is_personal;



CREATE INDEX workspace_model_routes_enabled_idx ON workspace_model_routes USING btree (workspace_id, "position") WHERE enabled;



ALTER TABLE ONLY agent_artifacts
    ADD CONSTRAINT agent_artifacts_session_fk FOREIGN KEY (app_name, user_id, session_id) REFERENCES agent_sessions(app_name, user_id, id) ON DELETE CASCADE;



ALTER TABLE ONLY agent_session_events
    ADD CONSTRAINT agent_session_events_session_fk FOREIGN KEY (app_name, user_id, session_id) REFERENCES agent_sessions(app_name, user_id, id) ON DELETE CASCADE;



ALTER TABLE ONLY card_schedules
    ADD CONSTRAINT card_schedules_card_fk FOREIGN KEY (workspace_id, card_id) REFERENCES cards(workspace_id, id) ON DELETE CASCADE;



ALTER TABLE ONLY card_schedules
    ADD CONSTRAINT card_schedules_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE;



ALTER TABLE ONLY card_turns
    ADD CONSTRAINT card_turns_cancelled_by_fk FOREIGN KEY (workspace_id, cancelled_by_source_id) REFERENCES sources(workspace_id, id);



ALTER TABLE ONLY card_turns
    ADD CONSTRAINT card_turns_card_fk FOREIGN KEY (workspace_id, card_id) REFERENCES cards(workspace_id, id) ON DELETE CASCADE;



ALTER TABLE ONLY card_turns
    ADD CONSTRAINT card_turns_created_by_fk FOREIGN KEY (workspace_id, created_by_source_id) REFERENCES sources(workspace_id, id);



ALTER TABLE ONLY card_turns
    ADD CONSTRAINT card_turns_schedule_fk FOREIGN KEY (workspace_id, card_id, schedule_id) REFERENCES card_schedules(workspace_id, card_id, id) ON DELETE RESTRICT;



ALTER TABLE ONLY card_turns
    ADD CONSTRAINT card_turns_source_event_fk FOREIGN KEY (workspace_id, card_id, source_event_id) REFERENCES events(workspace_id, card_id, id) ON DELETE SET NULL;



ALTER TABLE ONLY card_turns
    ADD CONSTRAINT card_turns_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE;



ALTER TABLE ONLY cards
    ADD CONSTRAINT cards_project_fk FOREIGN KEY (workspace_id, project_id) REFERENCES projects(workspace_id, id) ON DELETE CASCADE;



ALTER TABLE ONLY cards
    ADD CONSTRAINT cards_source_discussion_fk FOREIGN KEY (workspace_id, source_discussion_id) REFERENCES cards(workspace_id, id) ON DELETE SET NULL (source_discussion_id);



ALTER TABLE ONLY cards
    ADD CONSTRAINT cards_source_fk FOREIGN KEY (workspace_id, created_by_source_id) REFERENCES sources(workspace_id, id);



ALTER TABLE ONLY cards
    ADD CONSTRAINT cards_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE;



ALTER TABLE ONLY events
    ADD CONSTRAINT events_card_fk FOREIGN KEY (workspace_id, card_id) REFERENCES cards(workspace_id, id) ON DELETE CASCADE;



ALTER TABLE ONLY events
    ADD CONSTRAINT events_attempt_fk FOREIGN KEY (workspace_id, card_id, attempt_id) REFERENCES turn_attempts(workspace_id, card_id, id) ON DELETE SET NULL (attempt_id);



ALTER TABLE ONLY events
    ADD CONSTRAINT events_source_fk FOREIGN KEY (workspace_id, actor_source_id) REFERENCES sources(workspace_id, id);



ALTER TABLE ONLY events
    ADD CONSTRAINT events_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE;



ALTER TABLE ONLY model_auth_flows
    ADD CONSTRAINT model_auth_flows_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE;



ALTER TABLE ONLY model_oauth_credentials
    ADD CONSTRAINT model_oauth_credentials_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE;



ALTER TABLE ONLY nexusdock_auth_flows
    ADD CONSTRAINT nexusdock_auth_flows_human_account_id_fkey FOREIGN KEY (human_account_id) REFERENCES human_accounts(id) ON DELETE CASCADE;



ALTER TABLE ONLY nexusdock_auth_flows
    ADD CONSTRAINT nexusdock_auth_flows_source_fk FOREIGN KEY (workspace_id, source_id) REFERENCES sources(workspace_id, id) ON DELETE CASCADE;



ALTER TABLE ONLY nexusdock_auth_flows
    ADD CONSTRAINT nexusdock_auth_flows_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE;



ALTER TABLE ONLY oauth_authorization_codes
    ADD CONSTRAINT oauth_authorization_codes_grant_id_fkey FOREIGN KEY (grant_id) REFERENCES oauth_grants(id) ON DELETE CASCADE;



ALTER TABLE ONLY oauth_grants
    ADD CONSTRAINT oauth_grants_client_id_fkey FOREIGN KEY (client_id) REFERENCES oauth_clients(client_id) ON DELETE CASCADE;



ALTER TABLE ONLY oauth_grants
    ADD CONSTRAINT oauth_grants_human_membership_fk FOREIGN KEY (workspace_id, human_account_id, human_source_id) REFERENCES workspace_memberships(workspace_id, human_account_id, source_id) ON DELETE CASCADE;



ALTER TABLE ONLY oauth_grants
    ADD CONSTRAINT oauth_grants_source_fk FOREIGN KEY (workspace_id, source_id) REFERENCES sources(workspace_id, id) ON DELETE CASCADE;



ALTER TABLE ONLY oauth_grants
    ADD CONSTRAINT oauth_grants_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE;



ALTER TABLE ONLY oauth_tokens
    ADD CONSTRAINT oauth_tokens_grant_id_fkey FOREIGN KEY (grant_id) REFERENCES oauth_grants(id) ON DELETE CASCADE;



ALTER TABLE ONLY oidc_identities
    ADD CONSTRAINT oidc_identities_human_account_id_fkey FOREIGN KEY (human_account_id) REFERENCES human_accounts(id) ON DELETE CASCADE;



ALTER TABLE ONLY projects
    ADD CONSTRAINT projects_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE;



ALTER TABLE ONLY sources
    ADD CONSTRAINT sources_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE;



ALTER TABLE ONLY turn_attempts
    ADD CONSTRAINT turn_attempts_card_fk FOREIGN KEY (workspace_id, card_id) REFERENCES cards(workspace_id, id) ON DELETE CASCADE;



ALTER TABLE ONLY turn_attempts
    ADD CONSTRAINT turn_attempts_retry_of_fk FOREIGN KEY (workspace_id, card_id, retry_of_attempt_id) REFERENCES turn_attempts(workspace_id, card_id, id) ON DELETE RESTRICT;



ALTER TABLE ONLY turn_attempts
    ADD CONSTRAINT turn_attempts_runner_fk FOREIGN KEY (workspace_id, runner_source_id) REFERENCES sources(workspace_id, id);



ALTER TABLE ONLY turn_attempts
    ADD CONSTRAINT turn_attempts_turn_fk FOREIGN KEY (workspace_id, card_id, turn_id) REFERENCES card_turns(workspace_id, card_id, id) ON DELETE CASCADE;



ALTER TABLE ONLY turn_attempts
    ADD CONSTRAINT turn_attempts_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE;



ALTER TABLE ONLY web_sessions
    ADD CONSTRAINT web_sessions_membership_fk FOREIGN KEY (workspace_id, human_account_id, source_id) REFERENCES workspace_memberships(workspace_id, human_account_id, source_id) ON DELETE CASCADE;



ALTER TABLE ONLY workspace_agent_settings
    ADD CONSTRAINT workspace_agent_settings_source_fk FOREIGN KEY (workspace_id, source_id) REFERENCES sources(workspace_id, id) ON DELETE RESTRICT;



ALTER TABLE ONLY workspace_agent_settings
    ADD CONSTRAINT workspace_agent_settings_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE;



ALTER TABLE ONLY workspace_mcp_connections
    ADD CONSTRAINT workspace_mcp_connections_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE;



ALTER TABLE ONLY workspace_memberships
    ADD CONSTRAINT workspace_memberships_human_account_id_fkey FOREIGN KEY (human_account_id) REFERENCES human_accounts(id) ON DELETE CASCADE;



ALTER TABLE ONLY workspace_memberships
    ADD CONSTRAINT workspace_memberships_source_fk FOREIGN KEY (workspace_id, source_id) REFERENCES sources(workspace_id, id) ON DELETE CASCADE;



ALTER TABLE ONLY workspace_memberships
    ADD CONSTRAINT workspace_memberships_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE;



ALTER TABLE ONLY workspace_model_routes
    ADD CONSTRAINT workspace_model_routes_credential_fk FOREIGN KEY (workspace_id, credential_id) REFERENCES model_oauth_credentials(workspace_id, id) ON DELETE RESTRICT;



ALTER TABLE ONLY workspace_model_routes
    ADD CONSTRAINT workspace_model_routes_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE;



ALTER TABLE ONLY workspace_nexusdock_connections
    ADD CONSTRAINT workspace_nexusdock_connections_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE;





-- +goose Down

DROP TABLE IF EXISTS agent_artifacts, agent_session_events, agent_user_state, agent_app_state, agent_sessions, workspace_nexusdock_connections, nexusdock_auth_flows, nexusdock_oauth_clients, workspace_model_routes, model_auth_flows, model_oauth_credentials, workspace_mcp_connections, workspace_agent_settings, oauth_tokens, oauth_authorization_codes, oauth_grants, oauth_clients, web_auth_flows, web_sessions, workspace_memberships, oidc_identities, human_accounts, turn_attempts, card_turns, events, card_schedules, cards, projects, sources, workspaces CASCADE;
