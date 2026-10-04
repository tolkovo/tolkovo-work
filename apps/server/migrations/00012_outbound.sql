-- Outbound messages to channels (T-03, docs/02 §11.4).
--
-- messages.delivery_error belongs to internal/conversations, channel_peers
-- to internal/channels. The queue itself is a River job per message
-- (channels.send): no outbound_messages table, the job carries only ids.

-- +goose Up

-- Why a reply was not delivered (delivery_status = 'failed'): blocked,
-- chat_not_found, limited, rejected, channel_unavailable or failed.
ALTER TABLE messages ADD COLUMN delivery_error text CHECK (char_length(delivery_error) BETWEEN 1 AND 64);
-- Replies still waiting for their channel: a reply goes out only after the
-- earlier ones of its conversation, so the order in the chat holds.
CREATE INDEX messages_pending_idx ON messages (workspace_id, conversation_id, id)
    WHERE delivery_status = 'pending';
-- Replies stored before this migration were never queued: nothing would
-- send them, and later replies of their conversations would wait for them.
-- FORCE ROW LEVEL SECURITY hides every row from the owner as well, so it
-- is lifted for this one statement, inside the migration's transaction.
ALTER TABLE messages NO FORCE ROW LEVEL SECURITY;
UPDATE messages SET delivery_status = 'failed', delivery_error = 'failed'
WHERE delivery_status = 'pending';
ALTER TABLE messages FORCE ROW LEVEL SECURITY;

-- People a personal Telegram account has seen: MTProto needs the user id
-- with the access hash this account was given to write to them. The hash
-- is valid only for this account's session.
CREATE TABLE channel_peers (
    workspace_id  uuid NOT NULL,
    channel_id    uuid NOT NULL,
    user_id       bigint NOT NULL,
    access_hash   bigint NOT NULL,
    updated_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, channel_id, user_id),
    FOREIGN KEY (workspace_id, channel_id) REFERENCES channels (workspace_id, id) ON DELETE CASCADE
);

ALTER TABLE channel_peers ENABLE ROW LEVEL SECURITY;
ALTER TABLE channel_peers FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON channel_peers
    USING (workspace_id = app_current_workspace())
    WITH CHECK (workspace_id = app_current_workspace());

GRANT SELECT, INSERT, UPDATE, DELETE ON channel_peers TO tolkovo_app;

-- +goose Down
DROP TABLE channel_peers;
DROP INDEX messages_pending_idx;
ALTER TABLE messages DROP COLUMN delivery_error;
