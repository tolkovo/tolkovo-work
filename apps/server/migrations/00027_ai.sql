-- The AI base of phase 1 (AI-01, docs/03 §8.1, ADR-0008). Owned by
-- internal/ai.
--
-- A workspace brings its own key (BYOK) to Anthropic, OpenAI or an
-- OpenAI-compatible server and picks one model. The key lives in
-- ai_credentials, sealed by internal/platform/crypto with workspace_id and
-- the credential id as associated data. Every call to a model is a row of
-- ai_calls: tokens, cost in millionths of a US dollar, latency, outcome,
-- and the request and answer with personal data masked. The masked texts
-- are cleared after 30 days; the rows stay, the monthly budget counts them.

-- +goose Up

CREATE TABLE ai_credentials (
    id            uuid NOT NULL,
    workspace_id  uuid NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    -- The key opens only this provider's API.
    provider      text NOT NULL CHECK (provider IN ('anthropic', 'openai', 'openai_compatible')),
    sealed        bytea NOT NULL,
    -- "…" and the last four characters, the only form the panel sees.
    hint          text NOT NULL CHECK (char_length(hint) <= 8),
    created_by    uuid,
    created_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, id),
    FOREIGN KEY (workspace_id, created_by) REFERENCES memberships (workspace_id, user_id) ON DELETE SET NULL (created_by)
);

CREATE TABLE ai_settings (
    workspace_id           uuid PRIMARY KEY REFERENCES workspaces (id) ON DELETE CASCADE,
    enabled                boolean NOT NULL DEFAULT false,
    provider               text CHECK (provider IN ('anthropic', 'openai', 'openai_compatible')),
    model                  text CHECK (char_length(model) BETWEEN 1 AND 100),
    -- Only for openai_compatible: where the server is.
    base_url               text CHECK (char_length(base_url) <= 500),
    credential_id          uuid,
    -- Prices of the model per million tokens, in millionths of a US dollar.
    input_price_micros     bigint NOT NULL DEFAULT 0 CHECK (input_price_micros BETWEEN 0 AND 1000000000),
    output_price_micros    bigint NOT NULL DEFAULT 0 CHECK (output_price_micros BETWEEN 0 AND 1000000000),
    -- Per calendar month (UTC), in millionths of a US dollar; NULL: no limit.
    monthly_budget_micros  bigint CHECK (monthly_budget_micros BETWEEN 0 AND 1000000000000),
    updated_at             timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (workspace_id, credential_id) REFERENCES ai_credentials (workspace_id, id) ON DELETE SET NULL (credential_id),
    CONSTRAINT ai_settings_enabled_complete CHECK (NOT enabled OR (provider IS NOT NULL AND model IS NOT NULL))
);

CREATE TABLE ai_calls (
    id                 uuid PRIMARY KEY,
    workspace_id       uuid NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    conversation_id    uuid,
    -- What the call was for: connection_check, issue_draft, ...
    purpose            text NOT NULL CHECK (char_length(purpose) BETWEEN 1 AND 50),
    provider           text NOT NULL,
    model              text NOT NULL,
    input_tokens       integer NOT NULL DEFAULT 0 CHECK (input_tokens >= 0),
    output_tokens      integer NOT NULL DEFAULT 0 CHECK (output_tokens >= 0),
    cached_tokens      integer NOT NULL DEFAULT 0 CHECK (cached_tokens >= 0),
    cost_micros        bigint NOT NULL DEFAULT 0 CHECK (cost_micros >= 0),
    latency_ms         integer NOT NULL DEFAULT 0 CHECK (latency_ms >= 0),
    -- ok, or why there is no answer: error, timeout, refused (the model
    -- declined), truncated (out of tokens), bad_output (not the JSON asked).
    status             text NOT NULL CHECK (status IN ('ok', 'error', 'timeout', 'refused', 'truncated', 'bad_output')),
    -- The kind of error (auth, rate_limited, unavailable, rejected) and the
    -- provider's code; never its message, which may quote the key.
    error_code         text CHECK (char_length(error_code) <= 100),
    -- What went to the model and came back, personal data masked; cleared
    -- after 30 days.
    request_redacted   jsonb,
    response_redacted  jsonb,
    created_at         timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (workspace_id, conversation_id) REFERENCES conversations (workspace_id, id) ON DELETE SET NULL (conversation_id)
);

-- The month's spending of a workspace.
CREATE INDEX ai_calls_workspace_idx ON ai_calls (workspace_id, created_at);
-- Masked texts left to clear.
CREATE INDEX ai_calls_texts_idx ON ai_calls (created_at) WHERE request_redacted IS NOT NULL OR response_redacted IS NOT NULL;

ALTER TABLE ai_credentials ENABLE ROW LEVEL SECURITY;
ALTER TABLE ai_credentials FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON ai_credentials
    USING (workspace_id = app_current_workspace())
    WITH CHECK (workspace_id = app_current_workspace());

ALTER TABLE ai_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE ai_settings FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON ai_settings
    USING (workspace_id = app_current_workspace())
    WITH CHECK (workspace_id = app_current_workspace());

ALTER TABLE ai_calls ENABLE ROW LEVEL SECURITY;
ALTER TABLE ai_calls FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON ai_calls
    USING (workspace_id = app_current_workspace())
    WITH CHECK (workspace_id = app_current_workspace());
-- The worker clears the masked texts of old calls in every workspace,
-- nothing else.
CREATE POLICY system_see_old ON ai_calls FOR SELECT TO tolkovo_system
    USING (created_at < now() - interval '30 days');
CREATE POLICY system_clear_old ON ai_calls FOR UPDATE TO tolkovo_system
    USING (created_at < now() - interval '30 days');

GRANT SELECT, INSERT, DELETE ON ai_credentials TO tolkovo_app;
GRANT SELECT, INSERT, UPDATE ON ai_settings TO tolkovo_app;
-- Calls are a log: written once, never changed by the app.
GRANT SELECT, INSERT ON ai_calls TO tolkovo_app;
GRANT SELECT (id, created_at, request_redacted, response_redacted) ON ai_calls TO tolkovo_system;
GRANT UPDATE (request_redacted, response_redacted) ON ai_calls TO tolkovo_system;

-- +goose Down
DROP TABLE ai_calls;
DROP TABLE ai_settings;
DROP TABLE ai_credentials;
