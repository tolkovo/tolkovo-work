-- Tags of tasks (ADR-0015, docs/02 §12.5). Owned by internal/tasks.
--
-- What a task is about: design, interface, backend and so on. A list of the
-- workspace's own, apart from the tags of conversations: those sort what
-- customers ask, these sort the work. Any member adds a tag while tagging a
-- task; renaming and deleting are for owners and admins. A task has up to
-- 10 tags, and the list of tasks filters by one.

-- +goose Up

CREATE TABLE task_tags (
    id            uuid NOT NULL,
    workspace_id  uuid NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    name          text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 40),
    -- A palette key of the panel, as tags.color.
    color         text NOT NULL DEFAULT 'gray'
                  CHECK (color IN ('gray', 'teal', 'amber', 'red', 'violet', 'blue', 'green')),
    created_by    uuid,
    created_at    timestamptz NOT NULL,
    PRIMARY KEY (workspace_id, id),
    FOREIGN KEY (workspace_id, created_by) REFERENCES memberships (workspace_id, user_id) ON DELETE SET NULL (created_by)
);
CREATE UNIQUE INDEX task_tags_name_uq ON task_tags (workspace_id, lower(name));

CREATE TABLE task_tag_links (
    workspace_id  uuid NOT NULL,
    task_id       uuid NOT NULL,
    tag_id        uuid NOT NULL,
    created_at    timestamptz NOT NULL,
    PRIMARY KEY (workspace_id, task_id, tag_id),
    FOREIGN KEY (workspace_id, task_id) REFERENCES tasks (workspace_id, id) ON DELETE CASCADE,
    FOREIGN KEY (workspace_id, tag_id) REFERENCES task_tags (workspace_id, id) ON DELETE CASCADE
);
-- The tag filter of the list.
CREATE INDEX task_tag_links_tag_idx ON task_tag_links (workspace_id, tag_id, task_id);

-- The tags of a task changed: a line of its feed.
ALTER TABLE task_activity DROP CONSTRAINT task_activity_kind_check;
ALTER TABLE task_activity ADD CONSTRAINT task_activity_kind_check
    CHECK (kind IN ('comment', 'created', 'status', 'assignee', 'queue', 'priority', 'edited', 'linked', 'unlinked', 'files', 'topic', 'tags'));

ALTER TABLE task_tags ENABLE ROW LEVEL SECURITY;
ALTER TABLE task_tags FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON task_tags
    USING (workspace_id = app_current_workspace())
    WITH CHECK (workspace_id = app_current_workspace());

ALTER TABLE task_tag_links ENABLE ROW LEVEL SECURITY;
ALTER TABLE task_tag_links FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON task_tag_links
    USING (workspace_id = app_current_workspace())
    WITH CHECK (workspace_id = app_current_workspace());

GRANT SELECT, INSERT, UPDATE, DELETE ON task_tags TO tolkovo_app;
GRANT SELECT, INSERT, DELETE ON task_tag_links TO tolkovo_app;

-- +goose Down
ALTER TABLE task_activity NO FORCE ROW LEVEL SECURITY;
DELETE FROM task_activity WHERE kind = 'tags';
ALTER TABLE task_activity FORCE ROW LEVEL SECURITY;
ALTER TABLE task_activity DROP CONSTRAINT task_activity_kind_check;
ALTER TABLE task_activity ADD CONSTRAINT task_activity_kind_check
    CHECK (kind IN ('comment', 'created', 'status', 'assignee', 'queue', 'priority', 'edited', 'linked', 'unlinked', 'files', 'topic'));
DROP TABLE task_tag_links;
DROP TABLE task_tags;
