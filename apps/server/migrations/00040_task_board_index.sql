-- The columns of the board of tasks (ADR-0019): the tasks of a status in
-- the order of the board, the one waiting longest first.
--
-- NO TRANSACTION: the index is built CONCURRENTLY, which cannot run inside a
-- transaction, so writers of tasks are never blocked (docs/00 §6.6).

-- +goose NO TRANSACTION

-- +goose Up
CREATE INDEX CONCURRENTLY IF NOT EXISTS tasks_board_idx ON tasks (workspace_id, status, status_changed_at);

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS tasks_board_idx;
