-- +goose Up
-- 记录上游模型响应实际返回的模型名称（当路由使用别名或中转转发时，可用于区分请求模型与实际回答模型）
ALTER TABLE agent_model_calls
    ADD COLUMN response_model text NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE agent_model_calls
    DROP COLUMN IF EXISTS response_model;
