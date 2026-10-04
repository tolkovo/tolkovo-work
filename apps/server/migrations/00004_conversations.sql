-- Conversations core: contacts, conversations, messages, attachments
-- (docs/02 §7.2, state machine §8, files §5.4). Owned by internal/conversations.
--
-- Every table carries workspace_id, has the tenant_isolation policy of
-- 00001 and references other tenant rows through composite foreign keys
-- that include workspace_id, so a row can never point into another
-- workspace, not even by a bug (docs/02 §6.1).
--
-- Not here yet, on purpose:
-- - channels do not exist (T-01, AP-01): conversations.channel_id has no
--   foreign key until the channels migration adds it;
-- - messages are a plain table. Monthly partitioning (docs/02 §5.1) needs a
--   role that may create partitions at run time and is decided separately;
-- - contact_identities, tags and SLA fields arrive with the tasks that use them.

-- +goose Up

CREATE TABLE contacts (
    id            uuid PRIMARY KEY,
    workspace_id  uuid NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    name          text NOT NULL DEFAULT '' CHECK (char_length(name) <= 200),
    email         citext CHECK (char_length(email) BETWEEN 3 AND 254),
    phone         text CHECK (char_length(phone) BETWEEN 3 AND 32),
    locale        text CHECK (locale IN ('ru', 'en')),
    -- Id from the site's or app's identity verification (docs/02 §13.2, §11.6).
    external_id   text CHECK (char_length(external_id) BETWEEN 1 AND 255),
    attributes    jsonb NOT NULL DEFAULT '{}',
    blocked_at    timestamptz,
    deleted_at    timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, id)
);
-- Contact list, newest first (ids are UUIDv7).
CREATE INDEX contacts_list_idx ON contacts (workspace_id, id DESC) WHERE deleted_at IS NULL;

-- Human-readable conversation numbers, one counter per workspace. The row
-- lock taken by the upsert serialises numbering inside a workspace only.
CREATE TABLE conversation_counters (
    workspace_id  uuid PRIMARY KEY REFERENCES workspaces (id) ON DELETE CASCADE,
    last_number   bigint NOT NULL
);

CREATE TABLE conversations (
    id                  uuid PRIMARY KEY,
    workspace_id        uuid NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    number              bigint NOT NULL CHECK (number > 0),
    contact_id          uuid NOT NULL,
    -- Foreign key to channels comes with the channels migration (T-01, AP-01).
    channel_id          uuid,
    external_thread_id  text CHECK (char_length(external_thread_id) <= 255),
    -- Allowed transitions are enforced in Go (internal/conversations/status.go).
    status              text NOT NULL CHECK (status IN ('new', 'bot', 'queued', 'assigned', 'pending', 'resolved', 'closed')),
    priority            smallint NOT NULL DEFAULT 0 CHECK (priority BETWEEN 0 AND 3),
    line_level          smallint NOT NULL DEFAULT 1 CHECK (line_level BETWEEN 1 AND 3),
    assignee_id         uuid,
    subject             text CHECK (char_length(subject) <= 200),
    first_response_at   timestamptz,
    last_message_at     timestamptz NOT NULL DEFAULT now(),
    resolved_at         timestamptz,
    closed_at           timestamptz,
    -- Optimistic locking: every change bumps it (docs/02 §8).
    version             integer NOT NULL DEFAULT 1,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, number),
    FOREIGN KEY (workspace_id, contact_id) REFERENCES contacts (workspace_id, id),
    -- The assignee is a member of the same workspace.
    FOREIGN KEY (workspace_id, assignee_id) REFERENCES memberships (workspace_id, user_id),
    -- Someone works on an assigned or pending conversation; nobody owns one
    -- that waits for the bot or the queue. Resolved and closed keep the last
    -- assignee for reopening and history.
    CHECK (status NOT IN ('assigned', 'pending') OR assignee_id IS NOT NULL),
    CHECK (status NOT IN ('new', 'bot', 'queued') OR assignee_id IS NULL)
);
-- Inbox lists (docs/04 §3.1): by status, by assignee, everything; newest
-- activity first with (last_message_at, id) as the cursor.
CREATE INDEX conversations_status_idx ON conversations (workspace_id, status, last_message_at DESC, id DESC);
CREATE INDEX conversations_assignee_idx ON conversations (workspace_id, assignee_id, last_message_at DESC, id DESC);
CREATE INDEX conversations_recent_idx ON conversations (workspace_id, last_message_at DESC, id DESC);
CREATE INDEX conversations_contact_idx ON conversations (workspace_id, contact_id, id DESC);
-- One open conversation per external chat (docs/02 §7.2).
CREATE UNIQUE INDEX conversations_open_thread_uq ON conversations (workspace_id, channel_id, external_thread_id)
    WHERE status <> 'closed' AND channel_id IS NOT NULL AND external_thread_id IS NOT NULL;

CREATE TABLE messages (
    id                uuid PRIMARY KEY,
    workspace_id      uuid NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    conversation_id   uuid NOT NULL,
    direction         text NOT NULL CHECK (direction IN ('in', 'out')),
    sender_type       text NOT NULL CHECK (sender_type IN ('contact', 'operator', 'bot', 'system')),
    -- Membership user for operators, contact for contacts; NULL for system.
    sender_id         uuid,
    kind              text NOT NULL CHECK (kind IN ('text', 'media', 'note', 'event')),
    body              text NOT NULL DEFAULT '' CHECK (char_length(body) <= 10000),
    body_format       text NOT NULL DEFAULT 'plain' CHECK (body_format IN ('plain', 'markdown')),
    attachments_count integer NOT NULL DEFAULT 0 CHECK (attachments_count BETWEEN 0 AND 10),
    is_private        boolean NOT NULL DEFAULT false,
    external_id       text CHECK (char_length(external_id) <= 255),
    -- NULL when there is nothing to deliver (internal notes, conversations
    -- without a channel). The outbound queue (T-03) moves it on.
    delivery_status   text CHECK (delivery_status IN ('pending', 'sent', 'delivered', 'read', 'failed')),
    -- Idempotency-Key of the request that created the message (docs/00 §8).
    idempotency_key   text CHECK (char_length(idempotency_key) BETWEEN 1 AND 255),
    meta              jsonb NOT NULL DEFAULT '{}',
    created_at        timestamptz NOT NULL DEFAULT now(),
    edited_at         timestamptz,
    deleted_at        timestamptz,
    UNIQUE (workspace_id, id),
    FOREIGN KEY (workspace_id, conversation_id) REFERENCES conversations (workspace_id, id),
    -- An internal note is exactly a private message.
    CHECK ((kind = 'note') = is_private)
);
-- The conversation feed, newest first; ids are UUIDv7, so id is the cursor.
CREATE INDEX messages_feed_idx ON messages (workspace_id, conversation_id, id DESC);
CREATE UNIQUE INDEX messages_idempotency_uq ON messages (workspace_id, conversation_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;

CREATE TABLE attachments (
    id               uuid PRIMARY KEY,
    workspace_id     uuid NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    conversation_id  uuid NOT NULL,
    -- NULL while uploaded but not yet sent with a message.
    message_id       uuid,
    -- Key from storage.NewKey, always inside this workspace's prefix.
    storage_key      text NOT NULL UNIQUE
                     CHECK (storage_key LIKE 'ws/' || workspace_id::text || '/conv/%'),
    file_name        text NOT NULL CHECK (char_length(file_name) BETWEEN 1 AND 255),
    mime             text NOT NULL CHECK (char_length(mime) BETWEEN 3 AND 255),
    size             bigint NOT NULL CHECK (size > 0),
    sha256           bytea CHECK (octet_length(sha256) = 32),
    width            integer,
    height           integer,
    duration_ms      integer,
    status           text NOT NULL DEFAULT 'uploading'
                     CHECK (status IN ('uploading', 'scanning', 'ready', 'rejected', 'expired')),
    uploaded_by      uuid,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, id),
    FOREIGN KEY (workspace_id, conversation_id) REFERENCES conversations (workspace_id, id),
    FOREIGN KEY (workspace_id, message_id) REFERENCES messages (workspace_id, id),
    CHECK (message_id IS NULL OR status <> 'uploading')
);
CREATE INDEX attachments_message_idx ON attachments (workspace_id, message_id) WHERE message_id IS NOT NULL;
CREATE INDEX attachments_conversation_idx ON attachments (workspace_id, conversation_id);

ALTER TABLE contacts ENABLE ROW LEVEL SECURITY;
ALTER TABLE contacts FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON contacts
    USING (workspace_id = app_current_workspace())
    WITH CHECK (workspace_id = app_current_workspace());

ALTER TABLE conversation_counters ENABLE ROW LEVEL SECURITY;
ALTER TABLE conversation_counters FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON conversation_counters
    USING (workspace_id = app_current_workspace())
    WITH CHECK (workspace_id = app_current_workspace());

ALTER TABLE conversations ENABLE ROW LEVEL SECURITY;
ALTER TABLE conversations FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON conversations
    USING (workspace_id = app_current_workspace())
    WITH CHECK (workspace_id = app_current_workspace());

ALTER TABLE messages ENABLE ROW LEVEL SECURITY;
ALTER TABLE messages FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON messages
    USING (workspace_id = app_current_workspace())
    WITH CHECK (workspace_id = app_current_workspace());

ALTER TABLE attachments ENABLE ROW LEVEL SECURITY;
ALTER TABLE attachments FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON attachments
    USING (workspace_id = app_current_workspace())
    WITH CHECK (workspace_id = app_current_workspace());

-- No DELETE: messages are soft-deleted, and erasing a contact's data
-- (docs/02 §13.6) gets its own grant with the data_requests task.
GRANT SELECT, INSERT, UPDATE ON contacts, conversation_counters, conversations, messages, attachments TO tolkovo_app;

-- +goose Down
DROP TABLE attachments;
DROP TABLE messages;
DROP TABLE conversations;
DROP TABLE conversation_counters;
DROP TABLE contacts;
