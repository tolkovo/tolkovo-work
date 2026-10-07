-- Topics of conversations (docs/02 §7.3). Owned by internal/conversations.
--
-- The customer picks what the question is about before writing: buttons of
-- the Telegram bot, chips of the widget; the operator can change it. A
-- topic sets the conversation's priority, narrows automatic distribution to
-- its members (none: everyone) and gives tasks made from the conversation a
-- default queue. Lines (phase 2) will take over the members part.

-- +goose Up

CREATE TABLE topics (
    id             uuid NOT NULL,
    workspace_id   uuid NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    name           citext NOT NULL CHECK (char_length(name) BETWEEN 1 AND 40),
    -- A palette key of the panel, as tags.color.
    color          text NOT NULL DEFAULT 'gray'
                   CHECK (color IN ('gray', 'teal', 'amber', 'red', 'violet', 'blue', 'green')),
    -- The order of the buttons and chips.
    position       integer NOT NULL DEFAULT 0,
    -- conversations.priority given to a conversation of this topic.
    priority       smallint NOT NULL DEFAULT 0 CHECK (priority BETWEEN 0 AND 3),
    -- The queue of tasks created from its conversations (internal/tasks
    -- reads it); a deleted queue leaves none.
    task_queue_id  uuid,
    created_at     timestamptz NOT NULL,
    updated_at     timestamptz NOT NULL,
    PRIMARY KEY (workspace_id, id),
    UNIQUE (workspace_id, name),
    FOREIGN KEY (workspace_id, task_queue_id) REFERENCES task_queues (workspace_id, id) ON DELETE SET NULL (task_queue_id)
);

-- Who gets the topic's conversations from automatic distribution; a topic
-- without members goes to everyone.
CREATE TABLE topic_members (
    workspace_id  uuid NOT NULL,
    topic_id      uuid NOT NULL,
    user_id       uuid NOT NULL,
    PRIMARY KEY (workspace_id, topic_id, user_id),
    FOREIGN KEY (workspace_id, topic_id) REFERENCES topics (workspace_id, id) ON DELETE CASCADE,
    FOREIGN KEY (workspace_id, user_id) REFERENCES memberships (workspace_id, user_id) ON DELETE CASCADE
);

ALTER TABLE conversations
    ADD COLUMN topic_id uuid,
    -- When the customer was asked for the topic: asked once per conversation.
    ADD COLUMN topic_asked_at timestamptz,
    ADD FOREIGN KEY (workspace_id, topic_id) REFERENCES topics (workspace_id, id) ON DELETE SET NULL (topic_id);
-- The topic filter of the inbox.
CREATE INDEX conversations_topic_idx ON conversations (workspace_id, topic_id, last_message_at DESC, id DESC)
    WHERE topic_id IS NOT NULL;

ALTER TABLE tasks
    ADD COLUMN topic_id uuid,
    ADD FOREIGN KEY (workspace_id, topic_id) REFERENCES topics (workspace_id, id) ON DELETE SET NULL (topic_id);
CREATE INDEX tasks_topic_idx ON tasks (workspace_id, topic_id, number DESC) WHERE topic_id IS NOT NULL;

-- A task's topic changed: a line of its feed.
ALTER TABLE task_activity DROP CONSTRAINT task_activity_kind_check;
ALTER TABLE task_activity ADD CONSTRAINT task_activity_kind_check
    CHECK (kind IN ('comment', 'created', 'status', 'assignee', 'queue', 'priority', 'edited', 'linked', 'unlinked', 'files', 'topic'));

ALTER TABLE topics ENABLE ROW LEVEL SECURITY;
ALTER TABLE topics FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON topics
    USING (workspace_id = app_current_workspace())
    WITH CHECK (workspace_id = app_current_workspace());

ALTER TABLE topic_members ENABLE ROW LEVEL SECURITY;
ALTER TABLE topic_members FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON topic_members
    USING (workspace_id = app_current_workspace())
    WITH CHECK (workspace_id = app_current_workspace());

GRANT SELECT, INSERT, UPDATE, DELETE ON topics TO tolkovo_app;
GRANT SELECT, INSERT, DELETE ON topic_members TO tolkovo_app;

-- +goose Down
ALTER TABLE task_activity NO FORCE ROW LEVEL SECURITY;
DELETE FROM task_activity WHERE kind = 'topic';
ALTER TABLE task_activity FORCE ROW LEVEL SECURITY;
ALTER TABLE task_activity DROP CONSTRAINT task_activity_kind_check;
ALTER TABLE task_activity ADD CONSTRAINT task_activity_kind_check
    CHECK (kind IN ('comment', 'created', 'status', 'assignee', 'queue', 'priority', 'edited', 'linked', 'unlinked', 'files'));
DROP INDEX tasks_topic_idx;
ALTER TABLE tasks DROP COLUMN topic_id;
DROP INDEX conversations_topic_idx;
ALTER TABLE conversations DROP COLUMN topic_asked_at, DROP COLUMN topic_id;
DROP TABLE topic_members;
DROP TABLE topics;
