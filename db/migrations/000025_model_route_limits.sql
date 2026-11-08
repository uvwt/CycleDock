-- +goose Up
-- 扩展 workspace_model_routes：记录底层模型映射、上下文窗口、最大输入/输出限制及元数据来源。
-- 空值（NULL）表示未知，不得自动解释为某个固定默认容量。
ALTER TABLE workspace_model_routes
    ADD COLUMN base_model text NOT NULL DEFAULT '',
    ADD COLUMN context_window_tokens integer,
    ADD COLUMN max_input_tokens integer,
    ADD COLUMN max_output_tokens integer,
    ADD COLUMN limits_source text NOT NULL DEFAULT 'unknown',
    ADD COLUMN limits_verified_at timestamp with time zone,
    ADD CONSTRAINT workspace_model_routes_limits_source_check CHECK (
        limits_source IN ('user', 'provider', 'registry', 'unknown')
    ),
    ADD CONSTRAINT workspace_model_routes_limits_positive_check CHECK (
        (context_window_tokens IS NULL OR context_window_tokens > 0) AND
        (max_input_tokens IS NULL OR max_input_tokens > 0) AND
        (max_output_tokens IS NULL OR max_output_tokens > 0)
    ),
    ADD CONSTRAINT workspace_model_routes_limits_bounds_check CHECK (
        (context_window_tokens IS NULL OR max_input_tokens IS NULL OR max_input_tokens <= context_window_tokens) AND
        (context_window_tokens IS NULL OR max_output_tokens IS NULL OR max_output_tokens <= context_window_tokens)
    );

-- +goose Down
ALTER TABLE workspace_model_routes
    DROP CONSTRAINT IF EXISTS workspace_model_routes_limits_bounds_check,
    DROP CONSTRAINT IF EXISTS workspace_model_routes_limits_positive_check,
    DROP CONSTRAINT IF EXISTS workspace_model_routes_limits_source_check,
    DROP COLUMN IF EXISTS limits_verified_at,
    DROP COLUMN IF EXISTS limits_source,
    DROP COLUMN IF EXISTS max_output_tokens,
    DROP COLUMN IF EXISTS max_input_tokens,
    DROP COLUMN IF EXISTS context_window_tokens,
    DROP COLUMN IF EXISTS base_model;
