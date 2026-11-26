-- +goose Up
-- 扩展 card_turns.source 支持外部事件与一次性唤醒来源（trigger），
-- 复用现有 card_turns 队列、dispatch_mode（enqueue/interrupt）与安全守卫链路。
ALTER TABLE card_turns DROP CONSTRAINT card_turns_source_check;
ALTER TABLE card_turns ADD CONSTRAINT card_turns_source_check CHECK (
    source = ANY (ARRAY['human'::text, 'schedule'::text, 'system'::text, 'trigger'::text])
);

ALTER TABLE card_turns DROP CONSTRAINT card_turns_source_shape_ck;
ALTER TABLE card_turns ADD CONSTRAINT card_turns_source_shape_ck CHECK (
    ((source = 'human'::text) AND (created_by_source_id IS NOT NULL) AND (schedule_id IS NULL) AND (scheduled_for IS NULL))
    OR ((source = 'schedule'::text) AND (created_by_source_id IS NULL) AND (schedule_id IS NOT NULL) AND (scheduled_for IS NOT NULL) AND (dispatch_mode = 'enqueue'::text))
    OR ((source = 'system'::text) AND (created_by_source_id IS NOT NULL) AND (schedule_id IS NULL) AND (scheduled_for IS NULL) AND (dispatch_mode = ANY (ARRAY['enqueue'::text, 'interrupt'::text])))
    OR ((source = 'trigger'::text) AND (created_by_source_id IS NULL) AND (schedule_id IS NULL) AND (scheduled_for IS NULL) AND (dispatch_mode = ANY (ARRAY['enqueue'::text, 'interrupt'::text])))
);

CREATE UNIQUE INDEX card_turns_trigger_idempotency_uq
    ON card_turns (workspace_id, idempotency_key)
    WHERE (source = 'trigger'::text AND idempotency_key IS NOT NULL);

-- 持久化外部事件触发器（GitHub / CircleCI / 通用 JSON Webhook）。
-- 密钥仅以服务端 AES-256-GCM 密文落库，不保存明文。
CREATE TABLE workspace_triggers (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    name text NOT NULL CHECK (btrim(name) <> ''::text),
    provider text NOT NULL CHECK (provider = ANY (ARRAY['github'::text, 'circleci'::text, 'generic'::text])),
    auth_mode text NOT NULL CHECK (auth_mode = ANY (ARRAY['github_hmac_sha256'::text, 'circleci_hmac_sha256'::text, 'generic_hmac_sha256'::text, 'bearer'::text])),
    signature_header text NOT NULL DEFAULT ''::text,
    secret_ciphertext bytea NOT NULL CHECK (octet_length(secret_ciphertext) > 0),
    secret_hint text NOT NULL DEFAULT ''::text,
    enabled boolean NOT NULL DEFAULT true,
    routing_mode text NOT NULL CHECK (routing_mode = ANY (ARRAY['fixed_card'::text, 'match_watchers'::text, 'create_todo'::text])),
    target_project_id uuid,
    target_card_id uuid,
    dispatch_mode text NOT NULL DEFAULT 'enqueue'::text CHECK (dispatch_mode = ANY (ARRAY['enqueue'::text, 'interrupt'::text])),
    filter_config jsonb NOT NULL DEFAULT '{}'::jsonb,
    cooldown_seconds integer NOT NULL DEFAULT 0 CHECK (cooldown_seconds >= 0 AND cooldown_seconds <= 86400),
    last_triggered_at timestamp with time zone,
    created_by_source_id uuid,
    created_at timestamp with time zone NOT NULL DEFAULT now(),
    updated_at timestamp with time zone NOT NULL DEFAULT now(),
    CONSTRAINT workspace_triggers_workspace_id_id_key UNIQUE (workspace_id, id),
    CONSTRAINT workspace_triggers_provider_auth_ck CHECK (
        (provider = 'github'::text AND auth_mode = 'github_hmac_sha256'::text)
        OR (provider = 'circleci'::text AND auth_mode = 'circleci_hmac_sha256'::text)
        OR (provider = 'generic'::text AND auth_mode = ANY (ARRAY['generic_hmac_sha256'::text, 'bearer'::text]))
    ),
    CONSTRAINT workspace_triggers_routing_shape_ck CHECK (
        (routing_mode = 'fixed_card'::text AND target_card_id IS NOT NULL)
        OR (routing_mode = 'create_todo'::text AND target_project_id IS NOT NULL AND target_card_id IS NULL)
        OR (routing_mode = 'match_watchers'::text AND target_card_id IS NULL)
    ),
    CONSTRAINT workspace_triggers_project_fk FOREIGN KEY (workspace_id, target_project_id)
        REFERENCES projects(workspace_id, id) ON DELETE CASCADE,
    CONSTRAINT workspace_triggers_card_fk FOREIGN KEY (workspace_id, target_card_id)
        REFERENCES cards(workspace_id, id) ON DELETE CASCADE,
    CONSTRAINT workspace_triggers_source_fk FOREIGN KEY (workspace_id, created_by_source_id)
        REFERENCES sources(workspace_id, id) ON DELETE SET NULL (created_by_source_id)
);

CREATE INDEX workspace_triggers_workspace_created_idx
    ON workspace_triggers (workspace_id, created_at DESC);

-- 卡片一次性等待唤醒（外部事件 match_key 匹配或延时自唤醒）。
CREATE TABLE card_watchers (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    card_id uuid NOT NULL,
    kind text NOT NULL CHECK (kind = ANY (ARRAY['event'::text, 'wakeup'::text])),
    match_key text NOT NULL DEFAULT ''::text,
    note text NOT NULL DEFAULT ''::text,
    dispatch_mode text NOT NULL DEFAULT 'enqueue'::text CHECK (dispatch_mode = ANY (ARRAY['enqueue'::text, 'interrupt'::text])),
    status text NOT NULL DEFAULT 'armed'::text CHECK (status = ANY (ARRAY['armed'::text, 'triggered'::text, 'expired'::text, 'cancelled'::text])),
    expires_at timestamp with time zone NOT NULL,
    created_by_source_id uuid,
    created_by_attempt_id uuid,
    triggered_turn_id uuid,
    resolved_reason text NOT NULL DEFAULT ''::text,
    resolved_at timestamp with time zone,
    idempotency_key text,
    created_at timestamp with time zone NOT NULL DEFAULT now(),
    updated_at timestamp with time zone NOT NULL DEFAULT now(),
    CONSTRAINT card_watchers_workspace_id_id_key UNIQUE (workspace_id, id),
    CONSTRAINT card_watchers_kind_shape_ck CHECK (
        (kind = 'event'::text AND btrim(match_key) <> ''::text)
        OR (kind = 'wakeup'::text AND match_key = ''::text)
    ),
    CONSTRAINT card_watchers_card_fk FOREIGN KEY (workspace_id, card_id)
        REFERENCES cards(workspace_id, id) ON DELETE CASCADE,
    CONSTRAINT card_watchers_source_fk FOREIGN KEY (workspace_id, created_by_source_id)
        REFERENCES sources(workspace_id, id) ON DELETE SET NULL (created_by_source_id),
    CONSTRAINT card_watchers_attempt_fk FOREIGN KEY (workspace_id, card_id, created_by_attempt_id)
        REFERENCES turn_attempts(workspace_id, card_id, id) ON DELETE SET NULL (created_by_attempt_id),
    CONSTRAINT card_watchers_turn_fk FOREIGN KEY (workspace_id, card_id, triggered_turn_id)
        REFERENCES card_turns(workspace_id, card_id, id) ON DELETE SET NULL (triggered_turn_id)
);

CREATE INDEX card_watchers_workspace_match_armed_idx
    ON card_watchers (workspace_id, lower(match_key))
    WHERE (status = 'armed'::text AND kind = 'event'::text);

CREATE INDEX card_watchers_due_armed_idx
    ON card_watchers (workspace_id, expires_at)
    WHERE (status = 'armed'::text);

CREATE INDEX card_watchers_card_created_idx
    ON card_watchers (workspace_id, card_id, created_at DESC);

CREATE UNIQUE INDEX card_watchers_idempotency_uq
    ON card_watchers (workspace_id, card_id, idempotency_key)
    WHERE (idempotency_key IS NOT NULL);

-- Webhook 投递审计与幂等去重表。
-- 仅对验签通过的投递施加 (trigger_id, delivery_id) 唯一去重，避免未认证伪造请求提前占用合法 delivery_id。
CREATE TABLE trigger_deliveries (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    trigger_id uuid NOT NULL,
    delivery_id text NOT NULL CHECK (btrim(delivery_id) <> ''::text),
    provider text NOT NULL CHECK (provider = ANY (ARRAY['github'::text, 'circleci'::text, 'generic'::text])),
    event_type text NOT NULL DEFAULT ''::text,
    event_action text NOT NULL DEFAULT ''::text,
    signature_valid boolean NOT NULL,
    status text NOT NULL CHECK (status = ANY (ARRAY['triggered'::text, 'filtered'::text, 'ignored'::text, 'cooldown'::text, 'rejected'::text])),
    reason text NOT NULL DEFAULT ''::text,
    match_keys text[] NOT NULL DEFAULT '{}'::text[],
    payload_summary text NOT NULL DEFAULT ''::text,
    triggered_turn_id uuid,
    triggered_watcher_turn_ids uuid[] NOT NULL DEFAULT '{}'::uuid[],
    created_card_id uuid,
    received_at timestamp with time zone NOT NULL DEFAULT now(),
    CONSTRAINT trigger_deliveries_trigger_fk FOREIGN KEY (workspace_id, trigger_id)
        REFERENCES workspace_triggers(workspace_id, id) ON DELETE CASCADE,
    CONSTRAINT trigger_deliveries_turn_fk FOREIGN KEY (workspace_id, triggered_turn_id)
        REFERENCES card_turns(workspace_id, id) ON DELETE SET NULL (triggered_turn_id),
    CONSTRAINT trigger_deliveries_card_fk FOREIGN KEY (workspace_id, created_card_id)
        REFERENCES cards(workspace_id, id) ON DELETE SET NULL (created_card_id)
);

CREATE UNIQUE INDEX trigger_deliveries_verified_dedup_uq
    ON trigger_deliveries (trigger_id, delivery_id)
    WHERE (signature_valid = true);

CREATE INDEX trigger_deliveries_trigger_received_idx
    ON trigger_deliveries (workspace_id, trigger_id, received_at DESC);

-- +goose Down
DROP TABLE IF EXISTS trigger_deliveries;
DROP TABLE IF EXISTS card_watchers;
DROP TABLE IF EXISTS workspace_triggers;

DELETE FROM card_turns WHERE source = 'trigger';
DROP INDEX IF EXISTS card_turns_trigger_idempotency_uq;

ALTER TABLE card_turns DROP CONSTRAINT card_turns_source_shape_ck;
ALTER TABLE card_turns ADD CONSTRAINT card_turns_source_shape_ck CHECK (
    ((source = 'human'::text) AND (created_by_source_id IS NOT NULL) AND (schedule_id IS NULL) AND (scheduled_for IS NULL))
    OR ((source = 'schedule'::text) AND (created_by_source_id IS NULL) AND (schedule_id IS NOT NULL) AND (scheduled_for IS NOT NULL) AND (dispatch_mode = 'enqueue'::text))
    OR ((source = 'system'::text) AND (created_by_source_id IS NOT NULL) AND (schedule_id IS NULL) AND (scheduled_for IS NULL) AND (dispatch_mode = ANY (ARRAY['enqueue'::text, 'interrupt'::text])))
);

ALTER TABLE card_turns DROP CONSTRAINT card_turns_source_check;
ALTER TABLE card_turns ADD CONSTRAINT card_turns_source_check CHECK (
    source = ANY (ARRAY['human'::text, 'schedule'::text, 'system'::text])
);
