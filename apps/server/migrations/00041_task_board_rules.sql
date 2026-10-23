-- Rules of the board and blocked tasks (ADR-0019, K2). Owned by internal/tasks.
--
-- task_column_rules hold the soft limit of open tasks in a column and the
-- age after which a card stands too long, for the workspace (queue_id NULL)
-- or for a queue. Nothing is refused by them: the board only shows it.
--
-- A task is blocked while blocked_reason is set: a mark over any open
-- status, not a step of the path; closing the task takes it off. The feed
-- records both, the reason as the body of the blocked line.

-- +goose Up

CREATE TABLE task_column_rules (
    workspace_id  uuid NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    queue_id      uuid,
    status        text NOT NULL CHECK (status IN ('open', 'in_progress', 'review', 'verified')),
    wip_limit     integer CHECK (wip_limit BETWEEN 1 AND 999),
    age_days      integer CHECK (age_days BETWEEN 1 AND 365),
    CHECK (wip_limit IS NOT NULL OR age_days IS NOT NULL),
    FOREIGN KEY (workspace_id, queue_id) REFERENCES task_queues (workspace_id, id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX task_column_rules_uq ON task_column_rules (workspace_id, queue_id, status) NULLS NOT DISTINCT;

ALTER TABLE task_column_rules ENABLE ROW LEVEL SECURITY;
ALTER TABLE task_column_rules FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON task_column_rules
    USING (workspace_id = app_current_workspace())
    WITH CHECK (workspace_id = app_current_workspace());
GRANT SELECT, INSERT, UPDATE, DELETE ON task_column_rules TO tolkovo_app;

ALTER TABLE tasks ADD COLUMN blocked_reason text
    CHECK (char_length(blocked_reason) BETWEEN 1 AND 500);
ALTER TABLE tasks ADD CONSTRAINT tasks_blocked_open_check CHECK (blocked_reason IS NULL OR closed_at IS NULL);

ALTER TABLE task_activity DROP CONSTRAINT task_activity_kind_check;
ALTER TABLE task_activity ADD CONSTRAINT task_activity_kind_check
    CHECK (kind IN ('comment', 'created', 'status', 'assignee', 'queue', 'priority', 'edited', 'linked', 'unlinked', 'files', 'topic', 'tags',
                    'blocked', 'unblocked'));
-- The reason is the body of a blocked line, as the text is of a comment.
ALTER TABLE task_activity DROP CONSTRAINT task_activity_check;
ALTER TABLE task_activity ADD CONSTRAINT task_activity_check CHECK ((kind IN ('comment', 'blocked')) = (body IS NOT NULL));

-- +goose Down

ALTER TABLE task_activity NO FORCE ROW LEVEL SECURITY;
DELETE FROM task_activity WHERE kind IN ('blocked', 'unblocked');
ALTER TABLE task_activity FORCE ROW LEVEL SECURITY;
ALTER TABLE task_activity DROP CONSTRAINT task_activity_check;
ALTER TABLE task_activity ADD CONSTRAINT task_activity_check CHECK ((kind = 'comment') = (body IS NOT NULL));
ALTER TABLE task_activity DROP CONSTRAINT task_activity_kind_check;
ALTER TABLE task_activity ADD CONSTRAINT task_activity_kind_check
    CHECK (kind IN ('comment', 'created', 'status', 'assignee', 'queue', 'priority', 'edited', 'linked', 'unlinked', 'files', 'topic', 'tags'));
ALTER TABLE tasks DROP CONSTRAINT tasks_blocked_open_check;
ALTER TABLE tasks DROP COLUMN blocked_reason;
DROP TABLE task_column_rules;
