-- The programmer role and the return of a task for checking (ADR-0020).
--
-- programmer is a system role for the tasks section: tasks and the
-- conversations linked to them, no inbox (its permissions live in
-- platform/authz). A task put in review goes to its author for checking;
-- tasks.review_from keeps who worked on it, so the task goes back to them
-- when the check fails and it returns to in progress.

-- +goose Up

-- The policy of roles forbids writing system rows (workspace_id IS NULL)
-- and FORCE RLS holds the owner to it, so it is lifted for the seed.
ALTER TABLE roles NO FORCE ROW LEVEL SECURITY;
INSERT INTO roles (id, workspace_id, key, name, is_system) VALUES
    ('00000000-0000-7000-8000-000000000004', NULL, 'programmer', 'Programmer', true);
ALTER TABLE roles FORCE ROW LEVEL SECURITY;

ALTER TABLE tasks ADD COLUMN review_from uuid;
ALTER TABLE tasks ADD CONSTRAINT tasks_review_from_fkey
    FOREIGN KEY (workspace_id, review_from) REFERENCES memberships (workspace_id, user_id) ON DELETE SET NULL (review_from);

-- +goose Down

ALTER TABLE tasks DROP CONSTRAINT tasks_review_from_fkey;
ALTER TABLE tasks DROP COLUMN review_from;

-- Programmers become operators again. FORCE RLS hides the rows from the
-- owner, so it is lifted for the update.
ALTER TABLE memberships NO FORCE ROW LEVEL SECURITY;
ALTER TABLE invitations NO FORCE ROW LEVEL SECURITY;
UPDATE memberships SET role_id = '00000000-0000-7000-8000-000000000003'
WHERE role_id = '00000000-0000-7000-8000-000000000004';
UPDATE invitations SET role_id = '00000000-0000-7000-8000-000000000003'
WHERE role_id = '00000000-0000-7000-8000-000000000004';
ALTER TABLE invitations FORCE ROW LEVEL SECURITY;
ALTER TABLE memberships FORCE ROW LEVEL SECURITY;

ALTER TABLE roles NO FORCE ROW LEVEL SECURITY;
DELETE FROM roles WHERE id = '00000000-0000-7000-8000-000000000004';
ALTER TABLE roles FORCE ROW LEVEL SECURITY;
