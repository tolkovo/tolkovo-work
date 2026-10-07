-- Personal access tokens (ADR-0016, docs/02 §13.6). Owned by internal/tenancy,
-- checked by internal/auth.
--
-- A member issues a token to work with tasks from a script or an AI agent
-- (MCP) on their own behalf. A token acts as its member: it can do no more
-- than the member's current role allows, and no more than its scopes
-- (tasks:read, tasks:write). Only the SHA-256 of the token is kept; the
-- token holds 32 random bytes, so a fast hash is enough.

-- +goose Up

CREATE TABLE access_tokens (
    id            uuid NOT NULL,
    workspace_id  uuid NOT NULL,
    user_id       uuid NOT NULL,
    name          text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 80),
    scopes        text[] NOT NULL CHECK (
        cardinality(scopes) BETWEEN 1 AND 8 AND scopes <@ ARRAY['tasks:read', 'tasks:write']::text[]
    ),
    token_hash    bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    -- The first characters of the token, to tell tokens apart in the panel.
    prefix        text NOT NULL CHECK (char_length(prefix) BETWEEN 1 AND 32),
    created_at    timestamptz NOT NULL,
    expires_at    timestamptz,
    last_used_at  timestamptz,
    PRIMARY KEY (workspace_id, id),
    -- A member who leaves takes their tokens along.
    FOREIGN KEY (workspace_id, user_id) REFERENCES memberships (workspace_id, user_id) ON DELETE CASCADE
);
CREATE INDEX access_tokens_user_idx ON access_tokens (workspace_id, user_id, created_at);

ALTER TABLE access_tokens ENABLE ROW LEVEL SECURITY;
ALTER TABLE access_tokens FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON access_tokens
    USING (workspace_id = app_current_workspace())
    WITH CHECK (workspace_id = app_current_workspace());

-- Revoking deletes the row: nothing is kept that could be used again.
GRANT SELECT, INSERT, UPDATE (last_used_at), DELETE ON access_tokens TO tolkovo_app;

-- +goose Down
DROP TABLE access_tokens;
