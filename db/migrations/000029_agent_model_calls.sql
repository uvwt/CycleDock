-- +goose Up
CREATE TABLE agent_model_calls (
    id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    card_id uuid NOT NULL,
    turn_id uuid NOT NULL,
    attempt_id uuid NOT NULL,
    invocation_id text DEFAULT ''::text NOT NULL,
    call_index integer NOT NULL,
    purpose text NOT NULL,
    model_route_id uuid,
    model_id text NOT NULL,
    provider text NOT NULL,
    started_at timestamp with time zone NOT NULL,
    finished_at timestamp with time zone NOT NULL,
    duration_ms integer NOT NULL,
    status text NOT NULL,
    input_tokens integer NOT NULL DEFAULT 0,
    output_tokens integer NOT NULL DEFAULT 0,
    cached_input_tokens integer NOT NULL DEFAULT 0,
    reasoning_output_tokens integer NOT NULL DEFAULT 0,
    usage_source text NOT NULL DEFAULT 'unknown'::text,
    error_code text NOT NULL DEFAULT ''::text,
    error_summary text NOT NULL DEFAULT ''::text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT agent_model_calls_pkey PRIMARY KEY (workspace_id, card_id, id),
    CONSTRAINT agent_model_calls_duration_check CHECK (duration_ms >= 0),
    CONSTRAINT agent_model_calls_purpose_check CHECK (purpose IN ('agent', 'compaction')),
    CONSTRAINT agent_model_calls_status_check CHECK (status IN ('success', 'failed')),
    CONSTRAINT agent_model_calls_usage_source_check CHECK (usage_source IN ('actual', 'estimated', 'unknown')),
    CONSTRAINT agent_model_calls_card_fk FOREIGN KEY (workspace_id, card_id) REFERENCES cards(workspace_id, id) ON DELETE CASCADE,
    CONSTRAINT agent_model_calls_attempt_fk FOREIGN KEY (workspace_id, card_id, attempt_id) REFERENCES turn_attempts(workspace_id, card_id, id) ON DELETE CASCADE
);

CREATE INDEX agent_model_calls_attempt_idx ON agent_model_calls USING btree (workspace_id, card_id, attempt_id, call_index ASC);

-- +goose Down
DROP TABLE IF EXISTS agent_model_calls;

