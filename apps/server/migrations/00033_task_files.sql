-- Files of tasks: screenshots, logs, documents (ADR-0015, docs/02 §12.5).
-- Owned by internal/tasks.
--
-- A member registers a file and uploads it with a signed request; the row
-- has no task yet. Creating a task, a comment, or adding files to a task's
-- description takes it: the file is checked (its first bytes fit its type
-- and, with ClamAV, it is clean) and gets its task, and its comment when it
-- belongs to one. A file nobody took, a file removed from a description and
-- the files of a deleted task lose their task; the cleanup job deletes
-- their objects and rows.

-- +goose Up

CREATE TABLE task_files (
    id            uuid NOT NULL,
    workspace_id  uuid NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    task_id       uuid,
    -- The comment the file came with; NULL: the task's description.
    activity_id   uuid,
    storage_key   text NOT NULL UNIQUE CHECK (storage_key LIKE 'ws/' || workspace_id::text || '/tasks/%'),
    file_name     text NOT NULL CHECK (char_length(file_name) BETWEEN 1 AND 255),
    content_type  text NOT NULL CHECK (char_length(content_type) BETWEEN 3 AND 255),
    size          bigint NOT NULL CHECK (size > 0),
    uploaded_by   uuid,
    created_at    timestamptz NOT NULL,
    -- When a task took the file; it never goes back to NULL, so a file
    -- that lost its task is not taken again.
    attached_at   timestamptz,
    PRIMARY KEY (workspace_id, id),
    CHECK (task_id IS NULL OR attached_at IS NOT NULL),
    -- A deleted task leaves its files without a task (and its comments'
    -- files without a comment), for the cleanup.
    FOREIGN KEY (workspace_id, task_id) REFERENCES tasks (workspace_id, id) ON DELETE SET NULL (task_id),
    FOREIGN KEY (workspace_id, activity_id) REFERENCES task_activity (workspace_id, id) ON DELETE SET NULL (activity_id),
    FOREIGN KEY (workspace_id, uploaded_by) REFERENCES memberships (workspace_id, user_id) ON DELETE SET NULL (uploaded_by)
);
CREATE INDEX task_files_task_idx ON task_files (workspace_id, task_id, created_at) WHERE task_id IS NOT NULL;
-- What the cleanup looks for: files without a task.
CREATE INDEX task_files_loose_idx ON task_files (created_at) WHERE task_id IS NULL;

ALTER TABLE task_files ENABLE ROW LEVEL SECURITY;
ALTER TABLE task_files FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON task_files
    USING (workspace_id = app_current_workspace())
    WITH CHECK (workspace_id = app_current_workspace());
-- The worker finds files without a task in every workspace (ids only) and
-- cleans each one up in its tenant transaction.
CREATE POLICY system_see_loose ON task_files FOR SELECT TO tolkovo_system
    USING (task_id IS NULL);

GRANT SELECT, INSERT, UPDATE, DELETE ON task_files TO tolkovo_app;
GRANT SELECT (id, workspace_id, task_id, created_at, attached_at) ON task_files TO tolkovo_system;

-- Files added to a description or removed from it are a line of the feed.
ALTER TABLE task_activity DROP CONSTRAINT task_activity_kind_check;
ALTER TABLE task_activity ADD CONSTRAINT task_activity_kind_check
    CHECK (kind IN ('comment', 'created', 'status', 'assignee', 'queue', 'priority', 'edited', 'linked', 'unlinked', 'files'));

-- +goose Down
DROP TABLE task_files;
ALTER TABLE task_activity NO FORCE ROW LEVEL SECURITY;
DELETE FROM task_activity WHERE kind = 'files';
ALTER TABLE task_activity FORCE ROW LEVEL SECURITY;
ALTER TABLE task_activity DROP CONSTRAINT task_activity_kind_check;
ALTER TABLE task_activity ADD CONSTRAINT task_activity_kind_check
    CHECK (kind IN ('comment', 'created', 'status', 'assignee', 'queue', 'priority', 'edited', 'linked', 'unlinked'));
