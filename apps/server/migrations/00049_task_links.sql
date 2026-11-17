-- Links between tasks (ADR-0022, P3). Owned by internal/tasks.
--
-- blocks: from_task blocks to_task, which waits for it while it is open
-- (the «ждёт» plate); relates: the tasks are related, stored with the
-- smaller id first; duplicates: from_task repeats to_task, the original.
-- A pair of tasks has one link at most, in either direction: the server
-- checks the reverse one.
--
-- Links cross projects: who sees either task sees its link, the other
-- task shown by its status only when it is hidden (task_visible).

-- +goose Up

CREATE TABLE task_links (
    workspace_id uuid NOT NULL,
    from_task_id uuid NOT NULL,
    to_task_id   uuid NOT NULL,
    kind         text NOT NULL CHECK (kind IN ('blocks', 'relates', 'duplicates')),
    created_by   uuid,
    created_at   timestamptz NOT NULL,
    PRIMARY KEY (workspace_id, from_task_id, to_task_id),
    CHECK (from_task_id <> to_task_id),
    CHECK (kind <> 'relates' OR from_task_id < to_task_id),
    FOREIGN KEY (workspace_id, from_task_id) REFERENCES tasks (workspace_id, id) ON DELETE CASCADE,
    FOREIGN KEY (workspace_id, to_task_id) REFERENCES tasks (workspace_id, id) ON DELETE CASCADE
);
-- The links into a task: what it waits for, its duplicates.
CREATE INDEX task_links_to_idx ON task_links (workspace_id, to_task_id);

ALTER TABLE task_links ENABLE ROW LEVEL SECURITY;
ALTER TABLE task_links FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON task_links
    USING (workspace_id = app_current_workspace())
    WITH CHECK (workspace_id = app_current_workspace());
GRANT SELECT, INSERT, DELETE ON task_links TO tolkovo_app;

-- Whether a task waits for an open task that blocks it. Runs as the
-- caller, under the policies.
CREATE FUNCTION task_waiting(p_ws uuid, p_task uuid) RETURNS boolean
    LANGUAGE sql STABLE
    AS $$ SELECT EXISTS (SELECT 1 FROM task_links l
                         JOIN tasks b ON b.workspace_id = l.workspace_id AND b.id = l.from_task_id
                         WHERE l.workspace_id = p_ws AND l.to_task_id = p_task AND l.kind = 'blocks'
                           AND b.closed_at IS NULL) $$;
GRANT EXECUTE ON FUNCTION task_waiting(uuid, uuid) TO tolkovo_app;

-- Whether p_task waits, through blocks links, for p_blocker: then a new
-- link «p_task blocks p_blocker» would close a loop, which the server
-- refuses.
CREATE FUNCTION task_waits_for(p_ws uuid, p_task uuid, p_blocker uuid) RETURNS boolean
    LANGUAGE sql STABLE
    AS $$ WITH RECURSIVE up (id) AS (
              SELECT l.from_task_id FROM task_links l
              WHERE l.workspace_id = p_ws AND l.kind = 'blocks' AND l.to_task_id = p_task
              UNION
              SELECT l.from_task_id FROM task_links l
              JOIN up ON l.to_task_id = up.id
              WHERE l.workspace_id = p_ws AND l.kind = 'blocks'
          )
          SELECT EXISTS (SELECT 1 FROM up WHERE up.id = p_blocker) $$;
GRANT EXECUTE ON FUNCTION task_waits_for(uuid, uuid, uuid) TO tolkovo_app;

-- The feed says a link was added or removed, on both tasks.
ALTER TABLE task_activity DROP CONSTRAINT task_activity_kind_check;
ALTER TABLE task_activity ADD CONSTRAINT task_activity_kind_check
    CHECK (kind IN ('comment', 'created', 'status', 'assignee', 'queue', 'priority', 'edited', 'linked', 'unlinked', 'files', 'topic', 'tags',
                    'blocked', 'unblocked', 'link_added', 'link_removed'));

-- +goose Down

ALTER TABLE task_activity NO FORCE ROW LEVEL SECURITY;
DELETE FROM task_activity WHERE kind IN ('link_added', 'link_removed');
ALTER TABLE task_activity FORCE ROW LEVEL SECURITY;
ALTER TABLE task_activity DROP CONSTRAINT task_activity_kind_check;
ALTER TABLE task_activity ADD CONSTRAINT task_activity_kind_check
    CHECK (kind IN ('comment', 'created', 'status', 'assignee', 'queue', 'priority', 'edited', 'linked', 'unlinked', 'files', 'topic', 'tags',
                    'blocked', 'unblocked'));
DROP FUNCTION task_waits_for(uuid, uuid, uuid);
DROP FUNCTION task_waiting(uuid, uuid);
DROP TABLE task_links;
