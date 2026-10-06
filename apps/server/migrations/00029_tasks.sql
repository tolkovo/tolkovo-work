-- The tasks section (ADR-0015, docs/02 §12.5). Owned by internal/tasks.
--
-- A task for developers or the IT team, kept in Tolkovo next to GitHub
-- issues: a number in the workspace, a queue (optional), a status, a
-- priority and an assignee. Tasks are created from a conversation or by
-- hand; a task can be linked to several conversations (one bug, many
-- complaints), and an open one keeps them waiting for the fix
-- (conversations.awaiting_fix, docs/02 §8). Comments and changes are the
-- task's activity feed.

-- +goose Up

-- Human-readable task numbers, one counter per workspace, as for
-- conversations.
CREATE TABLE task_counters (
    workspace_id  uuid PRIMARY KEY REFERENCES workspaces (id) ON DELETE CASCADE,
    last_number   bigint NOT NULL
);

CREATE TABLE task_queues (
    id            uuid NOT NULL,
    workspace_id  uuid NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    name          text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 60),
    created_at    timestamptz NOT NULL,
    PRIMARY KEY (workspace_id, id)
);
CREATE UNIQUE INDEX task_queues_name_uq ON task_queues (workspace_id, lower(name));

-- Members told about new tasks without an assignee in the queue.
CREATE TABLE task_queue_members (
    workspace_id  uuid NOT NULL,
    queue_id      uuid NOT NULL,
    user_id       uuid NOT NULL,
    PRIMARY KEY (workspace_id, queue_id, user_id),
    FOREIGN KEY (workspace_id, queue_id) REFERENCES task_queues (workspace_id, id) ON DELETE CASCADE,
    FOREIGN KEY (workspace_id, user_id) REFERENCES memberships (workspace_id, user_id) ON DELETE CASCADE
);

CREATE TABLE tasks (
    id            uuid NOT NULL,
    workspace_id  uuid NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    number        bigint NOT NULL CHECK (number > 0),
    queue_id      uuid,
    title         text NOT NULL CHECK (char_length(title) BETWEEN 1 AND 256),
    body          text NOT NULL DEFAULT '' CHECK (char_length(body) <= 20000),
    -- open: new; in_progress: someone works on it; done: finished;
    -- canceled: will not be done.
    status        text NOT NULL CHECK (status IN ('open', 'in_progress', 'done', 'canceled')),
    priority      text NOT NULL DEFAULT 'normal' CHECK (priority IN ('low', 'normal', 'high', 'urgent')),
    assignee_id   uuid,
    created_by    uuid,
    created_at    timestamptz NOT NULL,
    updated_at    timestamptz NOT NULL,
    closed_at     timestamptz,
    version       integer NOT NULL DEFAULT 1,
    PRIMARY KEY (workspace_id, id),
    UNIQUE (workspace_id, number),
    CHECK ((status IN ('done', 'canceled')) = (closed_at IS NOT NULL)),
    FOREIGN KEY (workspace_id, queue_id) REFERENCES task_queues (workspace_id, id) ON DELETE SET NULL (queue_id),
    FOREIGN KEY (workspace_id, assignee_id) REFERENCES memberships (workspace_id, user_id) ON DELETE SET NULL (assignee_id),
    FOREIGN KEY (workspace_id, created_by) REFERENCES memberships (workspace_id, user_id) ON DELETE SET NULL (created_by)
);
-- The list: open first by the panel's filters, newest first.
CREATE INDEX tasks_list_idx ON tasks (workspace_id, number DESC);
CREATE INDEX tasks_assignee_idx ON tasks (workspace_id, assignee_id) WHERE closed_at IS NULL;

-- Conversations of a task. Deleting a contact's data deletes its
-- conversations and so these rows; the task stays.
CREATE TABLE task_conversations (
    workspace_id     uuid NOT NULL,
    task_id          uuid NOT NULL,
    conversation_id  uuid NOT NULL,
    created_by       uuid,
    created_at       timestamptz NOT NULL,
    PRIMARY KEY (workspace_id, task_id, conversation_id),
    FOREIGN KEY (workspace_id, task_id) REFERENCES tasks (workspace_id, id) ON DELETE CASCADE,
    FOREIGN KEY (workspace_id, conversation_id) REFERENCES conversations (workspace_id, id) ON DELETE CASCADE
);
CREATE INDEX task_conversations_conversation_idx ON task_conversations (workspace_id, conversation_id);

-- The feed of a task: comments and what changed (meta names the change,
-- only ids and enum values).
CREATE TABLE task_activity (
    id            uuid NOT NULL,
    workspace_id  uuid NOT NULL,
    task_id       uuid NOT NULL,
    actor_id      uuid,
    kind          text NOT NULL CHECK (kind IN ('comment', 'created', 'status', 'assignee', 'queue', 'priority', 'edited', 'linked', 'unlinked')),
    body          text CHECK (char_length(body) <= 4000),
    meta          jsonb NOT NULL DEFAULT '{}',
    created_at    timestamptz NOT NULL,
    PRIMARY KEY (workspace_id, id),
    CHECK ((kind = 'comment') = (body IS NOT NULL)),
    FOREIGN KEY (workspace_id, task_id) REFERENCES tasks (workspace_id, id) ON DELETE CASCADE
);
CREATE INDEX task_activity_task_idx ON task_activity (workspace_id, task_id, id);

-- Settings of the section; no row: the defaults.
CREATE TABLE task_settings (
    workspace_id         uuid PRIMARY KEY REFERENCES workspaces (id) ON DELETE CASCADE,
    enabled              boolean NOT NULL DEFAULT true,
    -- Which buttons a conversation shows: a task, a GitHub issue, or both.
    conversation_button  text NOT NULL DEFAULT 'both' CHECK (conversation_button IN ('tasks', 'github', 'both')),
    -- The reply about a done task; NULL: the built-in text.
    reply_ru             text CHECK (char_length(reply_ru) BETWEEN 1 AND 4000),
    reply_en             text CHECK (char_length(reply_en) BETWEEN 1 AND 4000),
    updated_at           timestamptz NOT NULL
);

ALTER TABLE task_counters ENABLE ROW LEVEL SECURITY;
ALTER TABLE task_counters FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON task_counters
    USING (workspace_id = app_current_workspace())
    WITH CHECK (workspace_id = app_current_workspace());

ALTER TABLE task_queues ENABLE ROW LEVEL SECURITY;
ALTER TABLE task_queues FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON task_queues
    USING (workspace_id = app_current_workspace())
    WITH CHECK (workspace_id = app_current_workspace());

ALTER TABLE task_queue_members ENABLE ROW LEVEL SECURITY;
ALTER TABLE task_queue_members FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON task_queue_members
    USING (workspace_id = app_current_workspace())
    WITH CHECK (workspace_id = app_current_workspace());

ALTER TABLE tasks ENABLE ROW LEVEL SECURITY;
ALTER TABLE tasks FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON tasks
    USING (workspace_id = app_current_workspace())
    WITH CHECK (workspace_id = app_current_workspace());

ALTER TABLE task_conversations ENABLE ROW LEVEL SECURITY;
ALTER TABLE task_conversations FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON task_conversations
    USING (workspace_id = app_current_workspace())
    WITH CHECK (workspace_id = app_current_workspace());

ALTER TABLE task_activity ENABLE ROW LEVEL SECURITY;
ALTER TABLE task_activity FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON task_activity
    USING (workspace_id = app_current_workspace())
    WITH CHECK (workspace_id = app_current_workspace());

ALTER TABLE task_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE task_settings FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON task_settings
    USING (workspace_id = app_current_workspace())
    WITH CHECK (workspace_id = app_current_workspace());

GRANT SELECT, INSERT, UPDATE ON task_counters TO tolkovo_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON task_queues TO tolkovo_app;
GRANT SELECT, INSERT, DELETE ON task_queue_members TO tolkovo_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON tasks TO tolkovo_app;
GRANT SELECT, INSERT, DELETE ON task_conversations TO tolkovo_app;
GRANT SELECT, INSERT ON task_activity TO tolkovo_app;
GRANT SELECT, INSERT, UPDATE ON task_settings TO tolkovo_app;

-- +goose Down
DROP TABLE task_settings;
DROP TABLE task_activity;
DROP TABLE task_conversations;
DROP TABLE tasks;
DROP TABLE task_queue_members;
DROP TABLE task_queues;
DROP TABLE task_counters;
