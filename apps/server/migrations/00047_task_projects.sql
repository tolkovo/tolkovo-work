-- Projects of the tasks section (ADR-0022, P1). Owned by internal/tasks.
--
-- A project is a board of its own inside «Задачи»: a name, a key that
-- starts the numbers of its tasks (CRM-41), an icon and a color for the
-- left rail, its members and its queues. Conversations, contacts, the
-- knowledge base and the team stay the workspace's.
--
-- Who sees a task: a member of its project, anyone when the project's
-- access is 'team', its author and its assignee, and the roles with
-- tasks.manage (task_visible below; the server passes the last one).
--
-- Every existing task goes to the default project «Задачи» (TSK, access
-- 'team') with its number: #12 becomes TSK-12. A workspace made later gets
-- its default project from the server on first use.
--
-- The numbers become unique per project in 00048, built CONCURRENTLY.

-- +goose Up

CREATE TABLE task_projects (
    id           uuid NOT NULL,
    workspace_id uuid NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    name         citext NOT NULL CHECK (char_length(name) BETWEEN 1 AND 60),
    key          text NOT NULL CHECK (key ~ '^[A-Z]{2,6}$'),
    -- One of the panel's project icons; NULL: the letters of the key.
    icon         text CHECK (icon IN ('chart', 'monitor', 'server', 'phone', 'cart', 'rocket', 'heart', 'globe')),
    -- A palette key of the panel, as task_tags.color.
    color        text NOT NULL DEFAULT 'blue'
                 CHECK (color IN ('gray', 'teal', 'amber', 'red', 'violet', 'blue', 'green')),
    -- members: its members see it; team: every member of the workspace.
    access       text NOT NULL DEFAULT 'members' CHECK (access IN ('members', 'team')),
    -- The project of old numbers (#12), of bare numbers and of tasks from
    -- topics without a project: exactly one per workspace.
    is_default   boolean NOT NULL DEFAULT false,
    -- The order in the switcher and the rail.
    position     integer NOT NULL DEFAULT 0,
    -- The counter of its task numbers.
    last_number  bigint NOT NULL DEFAULT 0 CHECK (last_number >= 0),
    archived_at  timestamptz,
    created_at   timestamptz NOT NULL,
    updated_at   timestamptz NOT NULL,
    PRIMARY KEY (workspace_id, id),
    UNIQUE (workspace_id, name),
    UNIQUE (workspace_id, key),
    CHECK (NOT is_default OR archived_at IS NULL)
);
CREATE UNIQUE INDEX task_projects_default_uq ON task_projects (workspace_id) WHERE is_default;

CREATE TABLE task_project_members (
    workspace_id uuid NOT NULL,
    project_id   uuid NOT NULL,
    user_id      uuid NOT NULL,
    created_at   timestamptz NOT NULL,
    PRIMARY KEY (workspace_id, project_id, user_id),
    FOREIGN KEY (workspace_id, project_id) REFERENCES task_projects (workspace_id, id) ON DELETE CASCADE,
    FOREIGN KEY (workspace_id, user_id) REFERENCES memberships (workspace_id, user_id) ON DELETE CASCADE
);
-- The projects of a member: the rail and every visibility check.
CREATE INDEX task_project_members_user_idx ON task_project_members (workspace_id, user_id);

-- Every workspace gets its default project now, with the counter of its
-- task numbers; the policies come after, so the owner writes these rows.
INSERT INTO task_projects (id, workspace_id, name, key, access, is_default, last_number, created_at, updated_at)
SELECT gen_random_uuid(), w.id, 'Задачи', 'TSK', 'team', true, coalesce(c.last_number, 0), now(), now()
FROM workspaces w
LEFT JOIN task_counters c ON c.workspace_id = w.id;

ALTER TABLE tasks ADD COLUMN project_id uuid;
ALTER TABLE task_queues ADD COLUMN project_id uuid;
ALTER TABLE topics ADD COLUMN task_project_id uuid;

-- FORCE ROW LEVEL SECURITY hides every row from the owner as well, so it
-- is lifted for the backfill, inside the migration's transaction.
ALTER TABLE tasks NO FORCE ROW LEVEL SECURITY;
ALTER TABLE task_queues NO FORCE ROW LEVEL SECURITY;
UPDATE tasks t SET project_id = p.id
FROM task_projects p WHERE p.workspace_id = t.workspace_id AND p.is_default;
UPDATE task_queues q SET project_id = p.id
FROM task_projects p WHERE p.workspace_id = q.workspace_id AND p.is_default;
ALTER TABLE task_queues FORCE ROW LEVEL SECURITY;
ALTER TABLE tasks FORCE ROW LEVEL SECURITY;

ALTER TABLE tasks ALTER COLUMN project_id SET NOT NULL;
ALTER TABLE tasks ADD CONSTRAINT tasks_project_fk
    FOREIGN KEY (workspace_id, project_id) REFERENCES task_projects (workspace_id, id);
ALTER TABLE task_queues ALTER COLUMN project_id SET NOT NULL;
ALTER TABLE task_queues ADD CONSTRAINT task_queues_project_fk
    FOREIGN KEY (workspace_id, project_id) REFERENCES task_projects (workspace_id, id) ON DELETE CASCADE;
-- A topic sends the tasks of its conversations to this project; NULL: the
-- default one.
ALTER TABLE topics ADD CONSTRAINT topics_task_project_fk
    FOREIGN KEY (workspace_id, task_project_id) REFERENCES task_projects (workspace_id, id) ON DELETE SET NULL (task_project_id);

-- Whether p_viewer sees a task of p_project made by p_author and given to
-- p_assignee; p_all is tasks.manage of the viewer's role. Runs as the
-- caller, under the policies of the tables it reads. (Arguments carry p_:
-- in a SQL function a column of the same name would win.)
CREATE FUNCTION task_visible(p_ws uuid, p_project uuid, p_author uuid, p_assignee uuid, p_viewer uuid, p_all boolean) RETURNS boolean
    LANGUAGE sql STABLE
    AS $$ SELECT coalesce(p_all, false)
              OR coalesce(p_viewer = p_author, false)
              OR coalesce(p_viewer = p_assignee, false)
              OR EXISTS (SELECT 1 FROM task_projects p
                         WHERE p.workspace_id = p_ws AND p.id = p_project AND p.access = 'team')
              OR EXISTS (SELECT 1 FROM task_project_members m
                         WHERE m.workspace_id = p_ws AND m.project_id = p_project AND m.user_id = p_viewer) $$;
GRANT EXECUTE ON FUNCTION task_visible(uuid, uuid, uuid, uuid, uuid, boolean) TO tolkovo_app;

ALTER TABLE task_projects ENABLE ROW LEVEL SECURITY;
ALTER TABLE task_projects FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON task_projects
    USING (workspace_id = app_current_workspace())
    WITH CHECK (workspace_id = app_current_workspace());

ALTER TABLE task_project_members ENABLE ROW LEVEL SECURITY;
ALTER TABLE task_project_members FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON task_project_members
    USING (workspace_id = app_current_workspace())
    WITH CHECK (workspace_id = app_current_workspace());

GRANT SELECT, INSERT, UPDATE, DELETE ON task_projects TO tolkovo_app;
GRANT SELECT, INSERT, DELETE ON task_project_members TO tolkovo_app;

-- +goose Down

-- The workspace counter goes on from the default project's; tasks of other
-- projects keep their numbers, which 00048's Down must find unique.
ALTER TABLE task_counters NO FORCE ROW LEVEL SECURITY;
ALTER TABLE task_projects NO FORCE ROW LEVEL SECURITY;
INSERT INTO task_counters (workspace_id, last_number)
SELECT workspace_id, last_number FROM task_projects WHERE is_default AND last_number > 0
ON CONFLICT (workspace_id) DO UPDATE SET last_number = greatest(task_counters.last_number, EXCLUDED.last_number);
ALTER TABLE task_counters FORCE ROW LEVEL SECURITY;
DROP FUNCTION task_visible(uuid, uuid, uuid, uuid, uuid, boolean);
ALTER TABLE topics DROP CONSTRAINT topics_task_project_fk;
ALTER TABLE topics DROP COLUMN task_project_id;
ALTER TABLE task_queues DROP CONSTRAINT task_queues_project_fk;
ALTER TABLE task_queues DROP COLUMN project_id;
ALTER TABLE tasks DROP CONSTRAINT tasks_project_fk;
ALTER TABLE tasks DROP COLUMN project_id;
DROP TABLE task_project_members;
DROP TABLE task_projects;
