-- Channels (T-01, docs/02 §11, ADR-0014). Owned by internal/channels.
--
-- A channel is where customers write: a Telegram bot (Bot API, token) or a
-- personal Telegram account (MTProto, signed in by phone). Secrets (the
-- bot token, the MTProto session) live in channel_secrets, sealed by
-- internal/platform/crypto with workspace_id and channel id as associated
-- data; channels holds only what the panel may show.

-- +goose Up

CREATE TABLE channels (
    id            uuid PRIMARY KEY,
    workspace_id  uuid NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    type          text NOT NULL CHECK (type IN ('telegram_bot', 'telegram_user')),
    name          text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 120),
    -- pending: a personal account between the phone and the last login
    -- step; active: works; error: needs the admin (error_code says why).
    status        text NOT NULL CHECK (status IN ('pending', 'active', 'error')),
    error_code    text CHECK (char_length(error_code) <= 64),
    -- Telegram id of the bot or the account. One bot or account serves one
    -- channel on the whole installation: two would fight over the webhook
    -- or the updates.
    external_id   text CHECK (char_length(external_id) <= 64),
    -- Not secret: username, display name, masked phone, delivery mode.
    config        jsonb NOT NULL DEFAULT '{}',
    created_by    uuid,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    -- Disconnected channels stay for the conversations that reference them.
    deleted_at    timestamptz,
    UNIQUE (workspace_id, id),
    FOREIGN KEY (workspace_id, created_by) REFERENCES memberships (workspace_id, user_id) ON DELETE SET NULL (created_by)
);

CREATE UNIQUE INDEX channels_external_uq ON channels (type, external_id)
    WHERE deleted_at IS NULL AND external_id IS NOT NULL;
CREATE INDEX channels_workspace_idx ON channels (workspace_id, created_at) WHERE deleted_at IS NULL;

CREATE TABLE channel_secrets (
    workspace_id  uuid NOT NULL,
    channel_id    uuid NOT NULL,
    sealed        bytea NOT NULL,
    updated_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, channel_id),
    FOREIGN KEY (workspace_id, channel_id) REFERENCES channels (workspace_id, id) ON DELETE CASCADE
);

ALTER TABLE conversations
    ADD CONSTRAINT conversations_channel_fk FOREIGN KEY (workspace_id, channel_id) REFERENCES channels (workspace_id, id);

ALTER TABLE channels ENABLE ROW LEVEL SECURITY;
ALTER TABLE channels FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON channels
    USING (workspace_id = app_current_workspace())
    WITH CHECK (workspace_id = app_current_workspace());
-- The worker drops personal-account logins left unfinished in any
-- workspace. Only pending rows, which nothing references yet.
CREATE POLICY system_prune_pending ON channels FOR DELETE TO tolkovo_system
    USING (status = 'pending');
CREATE POLICY system_see_pending ON channels FOR SELECT TO tolkovo_system
    USING (status = 'pending');

ALTER TABLE channel_secrets ENABLE ROW LEVEL SECURITY;
ALTER TABLE channel_secrets FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON channel_secrets
    USING (workspace_id = app_current_workspace())
    WITH CHECK (workspace_id = app_current_workspace());

GRANT SELECT, INSERT, UPDATE ON channels TO tolkovo_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON channel_secrets TO tolkovo_app;
-- Pending logins of the app's own workspace are deleted on restart of the
-- login; channel_secrets rows follow by cascade.
GRANT DELETE ON channels TO tolkovo_app;
GRANT SELECT, DELETE ON channels TO tolkovo_system;

-- +goose Down
ALTER TABLE conversations DROP CONSTRAINT conversations_channel_fk;
DROP TABLE channel_secrets;
DROP TABLE channels;
