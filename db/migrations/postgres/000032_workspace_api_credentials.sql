-- +goose Up
-- 自定义 OpenAI 兼容服务商凭据表：将 Base URL 与加密 API Key 提升为工作区级凭据实体，
-- 允许多个模型 Route 共享同一份加密凭据，并支持同一网关配置多个命名 Key。
CREATE TABLE workspace_api_credentials (
    id uuid NOT NULL PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    name text NOT NULL DEFAULT '',
    base_url text NOT NULL,
    api_key_env text NOT NULL DEFAULT '',
    api_key_ciphertext bytea,
    created_at timestamp with time zone NOT NULL DEFAULT now(),
    updated_at timestamp with time zone NOT NULL DEFAULT now(),
    CONSTRAINT workspace_api_credentials_workspace_id_id_key UNIQUE (workspace_id, id),
    CONSTRAINT workspace_api_credentials_base_url_ck CHECK (btrim(base_url) <> '')
);

CREATE INDEX idx_workspace_api_credentials_workspace
    ON workspace_api_credentials(workspace_id, created_at, id);

ALTER TABLE workspace_model_routes
    ADD COLUMN api_credential_id uuid;

-- 将存量 openai_compatible 路由按 (workspace_id, 归一化 base_url) 归并迁移至凭据表。
-- 优先选取已保存非空 api_key_ciphertext 的路由，并直接沿用该路由 ID 作为凭据 ID，
-- 使得既有 AES-GCM 密文的附加认证数据（workspace_id + id）可无缝解密，同时自动修复同地址缺失密钥的路由。
INSERT INTO workspace_api_credentials (
    id, workspace_id, name, base_url, api_key_env, api_key_ciphertext, created_at, updated_at
)
SELECT DISTINCT ON (
    r.workspace_id,
    COALESCE(NULLIF(rtrim(btrim(r.provider_base_url), '/'), ''), btrim(r.provider_base_url))
)
    r.id,
    r.workspace_id,
    COALESCE(
        NULLIF(substring(btrim(r.provider_base_url) FROM '^[a-zA-Z][a-zA-Z0-9+.-]*://([^/:]+)'), ''),
        btrim(r.provider_base_url)
    ),
    COALESCE(NULLIF(rtrim(btrim(r.provider_base_url), '/'), ''), btrim(r.provider_base_url)),
    btrim(r.api_key_env),
    r.api_key_ciphertext,
    r.created_at,
    r.updated_at
FROM workspace_model_routes r
WHERE r.provider_type = 'openai_compatible'
ORDER BY
    r.workspace_id,
    COALESCE(NULLIF(rtrim(btrim(r.provider_base_url), '/'), ''), btrim(r.provider_base_url)),
    (r.api_key_ciphertext IS NOT NULL AND octet_length(r.api_key_ciphertext) > 0) DESC,
    (btrim(r.api_key_env) <> '') DESC,
    r.updated_at DESC,
    r.position ASC,
    r.id ASC;

UPDATE workspace_model_routes r
SET api_credential_id = ac.id
FROM workspace_api_credentials ac
WHERE r.provider_type = 'openai_compatible'
  AND r.workspace_id = ac.workspace_id
  AND COALESCE(NULLIF(rtrim(btrim(r.provider_base_url), '/'), ''), btrim(r.provider_base_url)) = ac.base_url;

ALTER TABLE workspace_model_routes
    DROP CONSTRAINT workspace_model_routes_provider_ck,
    DROP COLUMN provider_base_url,
    DROP COLUMN api_key_env,
    DROP COLUMN api_key_ciphertext,
    ADD CONSTRAINT workspace_model_routes_api_credential_fk
        FOREIGN KEY (workspace_id, api_credential_id)
        REFERENCES workspace_api_credentials(workspace_id, id)
        ON DELETE CASCADE,
    ADD CONSTRAINT workspace_model_routes_provider_ck CHECK (
        (
            provider_type = 'openai_compatible'::text
            AND credential_id IS NULL
            AND api_credential_id IS NOT NULL
        ) OR (
            provider_type = ANY (ARRAY['chatgpt'::text, 'grok'::text])
            AND api_credential_id IS NULL
            AND ((NOT enabled) OR (credential_id IS NOT NULL))
        )
    );

CREATE INDEX idx_workspace_model_routes_api_credential
    ON workspace_model_routes(workspace_id, api_credential_id)
    WHERE api_credential_id IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_workspace_model_routes_api_credential;

ALTER TABLE workspace_model_routes
    ADD COLUMN provider_base_url text NOT NULL DEFAULT '',
    ADD COLUMN api_key_env text NOT NULL DEFAULT '',
    ADD COLUMN api_key_ciphertext bytea;

UPDATE workspace_model_routes r
SET provider_base_url = ac.base_url,
    api_key_env = ac.api_key_env,
    api_key_ciphertext = CASE WHEN r.id = ac.id THEN ac.api_key_ciphertext ELSE NULL END
FROM workspace_api_credentials ac
WHERE r.workspace_id = ac.workspace_id
  AND r.api_credential_id = ac.id;

ALTER TABLE workspace_model_routes
    DROP CONSTRAINT IF EXISTS workspace_model_routes_provider_ck,
    DROP CONSTRAINT IF EXISTS workspace_model_routes_api_credential_fk,
    DROP COLUMN IF EXISTS api_credential_id,
    ADD CONSTRAINT workspace_model_routes_provider_ck CHECK (
        (
            provider_type = 'openai_compatible'::text
            AND credential_id IS NULL
            AND btrim(provider_base_url) <> ''::text
        ) OR (
            provider_type = ANY (ARRAY['chatgpt'::text, 'grok'::text])
            AND btrim(provider_base_url) = ''::text
            AND btrim(api_key_env) = ''::text
            AND ((NOT enabled) OR (credential_id IS NOT NULL))
        )
    );

DROP TABLE IF EXISTS workspace_api_credentials;
