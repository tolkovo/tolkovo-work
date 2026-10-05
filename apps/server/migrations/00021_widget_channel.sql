-- The «Виджет» channel (W-01, docs/02 §13.2). The channel type is owned by
-- internal/channels, widget_sessions by internal/widget.
--
-- A chat on a company's site or inside its app (an Electron renderer). The
-- widget key is public and derived from the workspace and channel ids, so it
-- needs no column; the origins that may embed the widget live in
-- channels.config (allowed_origins), the identity secret sealed in
-- channel_secrets, as for the app channel. A visitor's chat is the thread of
-- the conversation (external_thread_id = the contact id), as for Telegram.

-- +goose Up

ALTER TABLE channels DROP CONSTRAINT channels_type_check;
ALTER TABLE channels ADD CONSTRAINT channels_type_check
    CHECK (type IN ('telegram_bot', 'telegram_user', 'app', 'widget'));

-- A visitor's session in one widget. The token is 32 random bytes and the
-- workspace id: the database keeps its SHA-256. An anonymous visitor gets a
-- contact only with the first message, so opening a page creates nothing in
-- the inbox; what they claimed before (a name, an email typed into the
-- site's JS API, unverified) waits in claim_* until then.
CREATE TABLE widget_sessions (
    id            uuid PRIMARY KEY,
    workspace_id  uuid NOT NULL,
    channel_id    uuid NOT NULL,
    contact_id    uuid,
    token_hash    bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    claimed       boolean NOT NULL DEFAULT false,
    claim_name    text CHECK (char_length(claim_name) <= 200),
    claim_email   text CHECK (char_length(claim_email) <= 254),
    created_at    timestamptz NOT NULL,
    last_seen_at  timestamptz NOT NULL,
    -- 30 days after the last activity; the worker deletes expired rows.
    expires_at    timestamptz NOT NULL,
    UNIQUE (workspace_id, id),
    FOREIGN KEY (workspace_id, channel_id) REFERENCES channels (workspace_id, id) ON DELETE CASCADE,
    FOREIGN KEY (workspace_id, contact_id) REFERENCES contacts (workspace_id, id) ON DELETE CASCADE
);

CREATE INDEX widget_sessions_expires_idx ON widget_sessions (expires_at);

ALTER TABLE widget_sessions ENABLE ROW LEVEL SECURITY;
ALTER TABLE widget_sessions FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON widget_sessions
    USING (workspace_id = app_current_workspace())
    WITH CHECK (workspace_id = app_current_workspace());
-- The worker deletes expired sessions in every workspace, nothing else.
CREATE POLICY system_prune_expired ON widget_sessions FOR DELETE TO tolkovo_system
    USING (expires_at < now());
CREATE POLICY system_see_expired ON widget_sessions FOR SELECT TO tolkovo_system
    USING (expires_at < now());

GRANT SELECT, INSERT, UPDATE, DELETE ON widget_sessions TO tolkovo_app;
GRANT SELECT, DELETE ON widget_sessions TO tolkovo_system;

-- +goose Down
DROP TABLE widget_sessions;
ALTER TABLE channels DROP CONSTRAINT channels_type_check;
-- NOT VALID: widget channels already created stay, with their conversations.
ALTER TABLE channels ADD CONSTRAINT channels_type_check
    CHECK (type IN ('telegram_bot', 'telegram_user', 'app')) NOT VALID;
