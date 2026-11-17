-- Moving tasks between projects and changing a project's key (ADR-0022,
-- P4). Owned by internal/tasks.
--
-- A moved task takes the next number of its new project; its old ref stays
-- in task_ref_aliases, so CRM-46 still opens what is APP-19 now. Numbers are
-- never given twice in a project, so an alias never meets a live number.
--
-- A changed key stays in task_project_key_aliases, so OLD-41 still opens
-- NEW-41; an old key is never given to another project.

-- +goose Up

CREATE TABLE task_ref_aliases (
    workspace_id uuid NOT NULL,
    project_id   uuid NOT NULL,
    number       bigint NOT NULL CHECK (number > 0),
    task_id      uuid NOT NULL,
    created_at   timestamptz NOT NULL,
    PRIMARY KEY (workspace_id, project_id, number),
    FOREIGN KEY (workspace_id, project_id) REFERENCES task_projects (workspace_id, id) ON DELETE CASCADE,
    FOREIGN KEY (workspace_id, task_id) REFERENCES tasks (workspace_id, id) ON DELETE CASCADE
);
-- The aliases of a task, for the cascade when it is deleted.
CREATE INDEX task_ref_aliases_task_idx ON task_ref_aliases (workspace_id, task_id);

CREATE TABLE task_project_key_aliases (
    workspace_id uuid NOT NULL,
    key          text NOT NULL CHECK (key ~ '^[A-Z]{2,6}$'),
    project_id   uuid NOT NULL,
    created_at   timestamptz NOT NULL,
    PRIMARY KEY (workspace_id, key),
    FOREIGN KEY (workspace_id, project_id) REFERENCES task_projects (workspace_id, id) ON DELETE CASCADE
);
CREATE INDEX task_project_key_aliases_project_idx ON task_project_key_aliases (workspace_id, project_id);

ALTER TABLE task_ref_aliases ENABLE ROW LEVEL SECURITY;
ALTER TABLE task_ref_aliases FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON task_ref_aliases
    USING (workspace_id = app_current_workspace())
    WITH CHECK (workspace_id = app_current_workspace());

ALTER TABLE task_project_key_aliases ENABLE ROW LEVEL SECURITY;
ALTER TABLE task_project_key_aliases FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON task_project_key_aliases
    USING (workspace_id = app_current_workspace())
    WITH CHECK (workspace_id = app_current_workspace());

GRANT SELECT, INSERT ON task_ref_aliases TO tolkovo_app;
GRANT SELECT, INSERT, DELETE ON task_project_key_aliases TO tolkovo_app;

ALTER TABLE task_activity DROP CONSTRAINT task_activity_kind_check;
ALTER TABLE task_activity ADD CONSTRAINT task_activity_kind_check
    CHECK (kind IN ('comment', 'created', 'status', 'assignee', 'queue', 'priority', 'edited', 'linked', 'unlinked', 'files', 'topic', 'tags',
                    'blocked', 'unblocked', 'link_added', 'link_removed', 'moved'));

-- +goose Down

ALTER TABLE task_activity NO FORCE ROW LEVEL SECURITY;
DELETE FROM task_activity WHERE kind = 'moved';
ALTER TABLE task_activity FORCE ROW LEVEL SECURITY;
ALTER TABLE task_activity DROP CONSTRAINT task_activity_kind_check;
ALTER TABLE task_activity ADD CONSTRAINT task_activity_kind_check
    CHECK (kind IN ('comment', 'created', 'status', 'assignee', 'queue', 'priority', 'edited', 'linked', 'unlinked', 'files', 'topic', 'tags',
                    'blocked', 'unblocked', 'link_added', 'link_removed'));
DROP TABLE task_project_key_aliases;
DROP TABLE task_ref_aliases;
