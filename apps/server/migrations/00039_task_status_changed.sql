-- The board of tasks (ADR-0019, docs/02 §12.5). Owned by internal/tasks.
--
-- status_changed_at is when the task came into its status: the board shows
-- how long a card stands in its column and puts the one waiting longest on
-- top. Existing tasks take it from their last status line in the feed, or
-- from their creation. Tasks are a small table (hundreds to thousands of
-- rows a workspace), so the fill runs here; NOT NULL goes through a
-- validated CHECK so setting it does not scan under the exclusive lock.

-- +goose Up

ALTER TABLE tasks ADD COLUMN status_changed_at timestamptz;

-- FORCE ROW LEVEL SECURITY hides every row from the owner as well, so it
-- is lifted for this one statement, inside the migration's transaction.
ALTER TABLE tasks NO FORCE ROW LEVEL SECURITY;
ALTER TABLE task_activity NO FORCE ROW LEVEL SECURITY;
UPDATE tasks t
SET status_changed_at = coalesce(
    (SELECT max(a.created_at) FROM task_activity a
     WHERE a.workspace_id = t.workspace_id AND a.task_id = t.id AND a.kind = 'status'),
    t.created_at);
ALTER TABLE task_activity FORCE ROW LEVEL SECURITY;
ALTER TABLE tasks FORCE ROW LEVEL SECURITY;

ALTER TABLE tasks ADD CONSTRAINT tasks_status_changed_at_nn CHECK (status_changed_at IS NOT NULL) NOT VALID;
ALTER TABLE tasks VALIDATE CONSTRAINT tasks_status_changed_at_nn;
ALTER TABLE tasks ALTER COLUMN status_changed_at SET NOT NULL;
ALTER TABLE tasks DROP CONSTRAINT tasks_status_changed_at_nn;

-- +goose Down

ALTER TABLE tasks DROP COLUMN status_changed_at;
