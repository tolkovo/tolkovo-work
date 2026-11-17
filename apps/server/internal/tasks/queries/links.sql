-- Links between tasks (ADR-0022, P3).

-- name: InsertTaskLink :execrows
INSERT INTO task_links (workspace_id, from_task_id, to_task_id, kind, created_by, created_at)
VALUES (@workspace_id, @from_task_id, @to_task_id, @kind, sqlc.narg(created_by), @created_at)
ON CONFLICT DO NOTHING;

-- name: TaskLinkBetween :one
-- The link of two tasks, in either direction.
SELECT * FROM task_links
WHERE workspace_id = @workspace_id
  AND ((from_task_id = @a AND to_task_id = @b) OR (from_task_id = @b AND to_task_id = @a));

-- name: DeleteTaskLink :one
DELETE FROM task_links
WHERE workspace_id = @workspace_id
  AND ((from_task_id = @a AND to_task_id = @b) OR (from_task_id = @b AND to_task_id = @a))
RETURNING *;

-- name: CountTaskLinks :one
SELECT count(*) FROM task_links
WHERE workspace_id = @workspace_id AND (from_task_id = @task_id OR to_task_id = @task_id);

-- name: ListTaskLinks :many
-- The links of a task, oldest first, with the other task and whether the
-- viewer sees it (task_visible): a hidden one shows by its status only.
SELECT l.kind, l.from_task_id, l.to_task_id, l.created_at,
       o.id AS other_id, o.project_id AS other_project_id, p.key AS other_project_key, o.number AS other_number,
       o.title AS other_title, o.status AS other_status,
       task_visible(o.workspace_id, o.project_id, o.created_by, o.assignee_id, @viewer::uuid, @see_all::boolean)::boolean AS visible
FROM task_links l
JOIN tasks o ON o.workspace_id = l.workspace_id
            AND o.id = CASE WHEN l.from_task_id = @task_id THEN l.to_task_id ELSE l.from_task_id END
JOIN task_projects p ON p.workspace_id = o.workspace_id AND p.id = o.project_id
WHERE l.workspace_id = @workspace_id AND (l.from_task_id = @task_id OR l.to_task_id = @task_id)
ORDER BY l.created_at, o.id;

-- name: WaitingOn :many
-- The open tasks that block a page of tasks, for the «ждёт» plate.
SELECT l.to_task_id AS task_id, b.id AS blocker_id, p.key AS project_key, b.number,
       task_visible(b.workspace_id, b.project_id, b.created_by, b.assignee_id, @viewer::uuid, @see_all::boolean)::boolean AS visible
FROM task_links l
JOIN tasks b ON b.workspace_id = l.workspace_id AND b.id = l.from_task_id
JOIN task_projects p ON p.workspace_id = b.workspace_id AND p.id = b.project_id
WHERE l.workspace_id = @workspace_id AND l.kind = 'blocks' AND l.to_task_id = ANY(@task_ids::uuid[])
  AND b.closed_at IS NULL
ORDER BY l.to_task_id, l.created_at, b.id;

-- name: BlockedBy :many
-- The open tasks a task blocks, and whether each still waits for another.
SELECT sqlc.embed(t), task_waiting(t.workspace_id, t.id)::boolean AS waiting
FROM tasks t
JOIN task_links l ON l.workspace_id = t.workspace_id AND l.to_task_id = t.id
WHERE l.workspace_id = @workspace_id AND l.from_task_id = @task_id AND l.kind = 'blocks' AND t.closed_at IS NULL
ORDER BY t.id;

-- name: BlocksPath :one
-- Whether from_task already waits, through blocks links, for to_task: a
-- new link to_task → from_task would close a loop.
SELECT task_waits_for(@workspace_id::uuid, @from_task_id::uuid, @to_task_id::uuid)::boolean;

-- name: VisibleTaskIDs :many
-- Which of these tasks the viewer sees, for the link lines of a feed.
SELECT t.id FROM tasks t
WHERE t.workspace_id = @workspace_id AND t.id = ANY(@ids::uuid[])
  AND task_visible(t.workspace_id, t.project_id, t.created_by, t.assignee_id, @viewer::uuid, @see_all::boolean);
