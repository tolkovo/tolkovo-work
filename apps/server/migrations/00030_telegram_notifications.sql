-- Telegram notifications of members (TK-02, ADR-0015). Owned by
-- internal/notifications.
--
-- A workspace adds a bot of its own just for notifications (not the bot of
-- a support channel): its token is sealed by internal/platform/crypto with
-- workspace_id and the row id as associated data. A member links their
-- Telegram by opening t.me/<bot>?start=<code> and pressing Start; the
-- server reads the bot's updates with getUpdates (no webhook, so it works
-- behind NAT) and keeps the chat id. Only the SHA-256 of a link code is
-- stored, for 15 minutes.

-- +goose Up

CREATE TABLE notify_bots (
    workspace_id    uuid PRIMARY KEY REFERENCES workspaces (id) ON DELETE CASCADE,
    -- The secret id of the sealed token (crypto.AAD).
    id              uuid NOT NULL,
    bot_id          bigint NOT NULL,
    username        text NOT NULL CHECK (char_length(username) BETWEEN 1 AND 64),
    sealed          bytea NOT NULL,
    -- The next update_id to read; getUpdates with it confirms the earlier ones.
    updates_offset  bigint NOT NULL DEFAULT 0,
    created_by      uuid,
    created_at      timestamptz NOT NULL,
    FOREIGN KEY (workspace_id, created_by) REFERENCES memberships (workspace_id, user_id) ON DELETE SET NULL (created_by)
);

CREATE TABLE telegram_links (
    workspace_id     uuid NOT NULL,
    user_id          uuid NOT NULL,
    -- Set once the member pressed Start in the bot.
    chat_id          bigint,
    telegram_name    text CHECK (char_length(telegram_name) <= 200),
    linked_at        timestamptz,
    -- A link waiting for Start: SHA-256 of the code and when it expires.
    code_hash        bytea,
    code_expires_at  timestamptz,
    PRIMARY KEY (workspace_id, user_id),
    CHECK ((chat_id IS NULL) = (linked_at IS NULL)),
    CHECK ((code_hash IS NULL) = (code_expires_at IS NULL)),
    FOREIGN KEY (workspace_id, user_id) REFERENCES memberships (workspace_id, user_id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX telegram_links_code_uq ON telegram_links (workspace_id, code_hash) WHERE code_hash IS NOT NULL;

ALTER TABLE notify_bots ENABLE ROW LEVEL SECURITY;
ALTER TABLE notify_bots FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON notify_bots
    USING (workspace_id = app_current_workspace())
    WITH CHECK (workspace_id = app_current_workspace());

ALTER TABLE telegram_links ENABLE ROW LEVEL SECURITY;
ALTER TABLE telegram_links FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON telegram_links
    USING (workspace_id = app_current_workspace())
    WITH CHECK (workspace_id = app_current_workspace());

GRANT SELECT, INSERT, UPDATE, DELETE ON notify_bots TO tolkovo_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON telegram_links TO tolkovo_app;

-- +goose Down
DROP TABLE telegram_links;
DROP TABLE notify_bots;
