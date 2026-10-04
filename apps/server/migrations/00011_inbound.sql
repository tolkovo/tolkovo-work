-- Inbound messages from channels (T-02, docs/02 §11.2, ADR-0014).
--
-- contact_identities and the message indexes belong to
-- internal/conversations; channels.last_inbound_at,
-- channel_update_state and the receiver policy to internal/channels.
--
-- Deduplication: Telegram ids a message within its chat, and a chat is one
-- thread (conversations.external_thread_id), so a message is unique by
-- (conversation, external_id). A retried update finds its message in any
-- conversation of the thread, closed ones included, through
-- conversations_thread_idx; no separate inbound_dedup table is needed
-- while messages is not partitioned (docs/02 §5.1).

-- +goose Up

-- Who a contact is in a channel: the Telegram user id of the person who
-- writes to the bot or the account (docs/02 §7.2).
CREATE TABLE contact_identities (
    id                uuid PRIMARY KEY,
    workspace_id      uuid NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    contact_id        uuid NOT NULL,
    channel_id        uuid NOT NULL,
    external_user_id  text NOT NULL CHECK (char_length(external_user_id) BETWEEN 1 AND 64),
    -- Without "@"; it changes, the user id does not.
    username          text CHECK (char_length(username) BETWEEN 1 AND 64),
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, channel_id, external_user_id),
    FOREIGN KEY (workspace_id, contact_id) REFERENCES contacts (workspace_id, id),
    FOREIGN KEY (workspace_id, channel_id) REFERENCES channels (workspace_id, id)
);
CREATE INDEX contact_identities_contact_idx ON contact_identities (workspace_id, contact_id);

-- Every conversation of an external chat, closed ones included.
CREATE INDEX conversations_thread_idx ON conversations (workspace_id, channel_id, external_thread_id)
    WHERE channel_id IS NOT NULL AND external_thread_id IS NOT NULL;
CREATE UNIQUE INDEX messages_external_uq ON messages (workspace_id, conversation_id, external_id)
    WHERE external_id IS NOT NULL;

-- When the channel last received a message: the connection check in the
-- wizard and the health of the channel. Updated at most once a minute.
ALTER TABLE channels ADD COLUMN last_inbound_at timestamptz;

-- Update state of a personal account (MTProto pts, qts, date, seq), so a
-- restart fetches what it missed with updates.getDifference.
CREATE TABLE channel_update_state (
    workspace_id  uuid NOT NULL,
    channel_id    uuid NOT NULL,
    pts           integer NOT NULL,
    qts           integer NOT NULL,
    date          integer NOT NULL,
    seq           integer NOT NULL,
    updated_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, channel_id),
    FOREIGN KEY (workspace_id, channel_id) REFERENCES channels (workspace_id, id) ON DELETE CASCADE
);

ALTER TABLE contact_identities ENABLE ROW LEVEL SECURITY;
ALTER TABLE contact_identities FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON contact_identities
    USING (workspace_id = app_current_workspace())
    WITH CHECK (workspace_id = app_current_workspace());

ALTER TABLE channel_update_state ENABLE ROW LEVEL SECURITY;
ALTER TABLE channel_update_state FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON channel_update_state
    USING (workspace_id = app_current_workspace())
    WITH CHECK (workspace_id = app_current_workspace());

-- The worker lists the channels it must hold a connection for, across
-- workspaces: active ones only. Receiving itself runs in the channel's
-- tenant transaction as tolkovo_app.
CREATE POLICY system_see_active ON channels FOR SELECT TO tolkovo_system
    USING (status = 'active' AND deleted_at IS NULL);

GRANT SELECT, INSERT, UPDATE ON contact_identities TO tolkovo_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON channel_update_state TO tolkovo_app;

-- +goose Down
DROP POLICY system_see_active ON channels;
DROP TABLE channel_update_state;
ALTER TABLE channels DROP COLUMN last_inbound_at;
DROP INDEX messages_external_uq;
DROP INDEX conversations_thread_idx;
DROP TABLE contact_identities;
