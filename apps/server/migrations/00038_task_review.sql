-- Review of tasks before production (ADR-0015, docs/02 §12.5). Owned by
-- internal/tasks.
--
-- Two statuses between the work and done: review (on the dev server, being
-- checked) and verified (checked, waits for the release); done then means
-- "in production". Both are open: the task is not closed, its conversations
-- keep waiting. Any status may follow any other, the panel only suggests the
-- next one.

-- +goose Up

ALTER TABLE tasks DROP CONSTRAINT tasks_status_check;
ALTER TABLE tasks ADD CONSTRAINT tasks_status_check
    CHECK (status IN ('open', 'in_progress', 'review', 'verified', 'done', 'canceled'));

-- +goose Down

UPDATE tasks SET status = 'in_progress' WHERE status IN ('review', 'verified');
ALTER TABLE tasks DROP CONSTRAINT tasks_status_check;
ALTER TABLE tasks ADD CONSTRAINT tasks_status_check
    CHECK (status IN ('open', 'in_progress', 'done', 'canceled'));
