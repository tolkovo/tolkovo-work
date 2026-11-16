-- Projects of tasks (migration 00047, ADR-0022).

-- name: EnsureDefaultProject :exec
-- The default project of a workspace made after 00047; nothing when it has one.
INSERT INTO task_projects (id, workspace_id, name, key, access, is_default, created_at, updated_at)
VALUES (@id, @workspace_id, @name, @key, 'team', true, @created_at, @created_at)
ON CONFLICT DO NOTHING;

-- name: DefaultProject :one
SELECT * FROM task_projects WHERE workspace_id = @workspace_id AND is_default;

-- name: ListProjects :many
-- Every project of the workspace in the switcher's order, with its members
-- and open tasks; the number is bounded by MaxProjects.
SELECT sqlc.embed(p),
       coalesce((SELECT array_agg(m.user_id ORDER BY m.user_id) FROM task_project_members m
                 WHERE m.workspace_id = p.workspace_id AND m.project_id = p.id), '{}')::uuid[] AS member_ids,
       (SELECT count(*) FROM tasks t
        WHERE t.workspace_id = p.workspace_id AND t.project_id = p.id AND t.closed_at IS NULL) AS open_tasks
FROM task_projects p
WHERE p.workspace_id = @workspace_id
ORDER BY p.archived_at IS NOT NULL, p.is_default DESC, p.position, lower(p.name), p.id;

-- name: ProjectKeys :many
-- The keys of every project, to write the refs of a page of tasks.
SELECT id, key FROM task_projects WHERE workspace_id = @workspace_id;

-- name: GetProject :one
SELECT * FROM task_projects WHERE workspace_id = @workspace_id AND id = @id;

-- name: GetProjectForUpdate :one
SELECT * FROM task_projects WHERE workspace_id = @workspace_id AND id = @id FOR UPDATE;

-- name: ProjectByKey :one
SELECT * FROM task_projects WHERE workspace_id = @workspace_id AND key = @key;

-- name: CountProjects :one
SELECT count(*) FROM task_projects WHERE workspace_id = @workspace_id;

-- name: InsertProject :one
INSERT INTO task_projects (id, workspace_id, name, key, icon, color, access, position, created_at, updated_at)
VALUES (@id, @workspace_id, @name, @key, sqlc.narg(icon), @color, @access, @position, @created_at, @created_at)
RETURNING *;

-- name: UpdateProject :one
UPDATE task_projects
SET name = @name, icon = sqlc.narg(icon), color = @color, access = @access, position = @position,
    archived_at = sqlc.narg(archived_at), updated_at = @updated_at
WHERE workspace_id = @workspace_id AND id = @id
RETURNING *;

-- name: DeleteEmptyProject :execrows
-- Removes a project without tasks; its queues and members go with it.
DELETE FROM task_projects p
WHERE p.workspace_id = @workspace_id AND p.id = @id AND NOT p.is_default
  AND NOT EXISTS (SELECT 1 FROM tasks t WHERE t.workspace_id = p.workspace_id AND t.project_id = p.id);

-- name: NextProjectNumber :one
-- The number of a new task in the project; the row lock orders the writers.
UPDATE task_projects SET last_number = last_number + 1
WHERE workspace_id = @workspace_id AND id = @id
RETURNING last_number;

-- name: ClearProjectMembers :exec
DELETE FROM task_project_members WHERE workspace_id = @workspace_id AND project_id = @project_id;

-- name: AddProjectMember :exec
INSERT INTO task_project_members (workspace_id, project_id, user_id, created_at)
VALUES (@workspace_id, @project_id, @user_id, @created_at)
ON CONFLICT DO NOTHING;

-- name: IsProjectMember :one
-- Whether the member works in the project: one of its members, or anyone
-- in a project open to the team. The assignee of its tasks must be.
SELECT EXISTS (
    SELECT 1 FROM task_projects p
    WHERE p.workspace_id = @workspace_id AND p.id = @project_id
      AND (p.access = 'team' OR EXISTS (
           SELECT 1 FROM task_project_members m
           WHERE m.workspace_id = p.workspace_id AND m.project_id = p.id AND m.user_id = @user_id))
);

-- name: TaskVisible :one
-- Whether the viewer sees the task (task_visible); no row: no such task.
SELECT task_visible(t.workspace_id, t.project_id, t.created_by, t.assignee_id, @viewer::uuid, @see_all::boolean)::boolean AS visible
FROM tasks t WHERE t.workspace_id = @workspace_id AND t.id = @id;

-- name: RemoveProjectMember :exec
-- A member who left the team is in no project.
DELETE FROM task_project_members WHERE workspace_id = @workspace_id AND user_id = @user_id;
