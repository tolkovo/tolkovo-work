-- Numbers of tasks per project (ADR-0022, P1): TSK-12 and CRM-12 may both
-- exist, so a number is unique in its project, not in the workspace. The
-- board and the lists read by project, and a queue's name is unique in its
-- project.
--
-- NO TRANSACTION: the indexes are built CONCURRENTLY, which cannot run
-- inside a transaction, so writers of tasks are never blocked (docs/00 §6.6).
-- The old constraint goes only after the new index stands.

-- +goose NO TRANSACTION

-- +goose Up
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS tasks_project_number_uq ON tasks (workspace_id, project_id, number);
CREATE INDEX CONCURRENTLY IF NOT EXISTS tasks_project_board_idx ON tasks (workspace_id, project_id, status, status_changed_at);
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS task_queues_project_name_uq ON task_queues (workspace_id, project_id, lower(name));
ALTER TABLE tasks DROP CONSTRAINT IF EXISTS tasks_workspace_id_number_key;
DROP INDEX CONCURRENTLY IF EXISTS task_queues_name_uq;

-- +goose Down
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS task_queues_name_uq ON task_queues (workspace_id, lower(name));
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS tasks_workspace_id_number_key ON tasks (workspace_id, number);
DROP INDEX CONCURRENTLY IF EXISTS task_queues_project_name_uq;
DROP INDEX CONCURRENTLY IF EXISTS tasks_project_board_idx;
DROP INDEX CONCURRENTLY IF EXISTS tasks_project_number_uq;
