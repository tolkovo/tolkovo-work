-- name: NextTaskNumber :one
INSERT INTO task_counters (workspace_id, last_number)
VALUES (@workspace_id, 1)
ON CONFLICT (workspace_id) DO UPDATE SET last_number = task_counters.last_number + 1
RETURNING last_number;

-- name: InsertTask :one
INSERT INTO tasks (id, workspace_id, number, queue_id, title, body, status, priority, assignee_id, created_by, created_at, updated_at)
VALUES (@id, @workspace_id, @number, sqlc.narg(queue_id), @title, @body, 'open', @priority, sqlc.narg(assignee_id), sqlc.narg(created_by), @created_at, @created_at)
RETURNING *;

-- name: GetTask :one
SELECT * FROM tasks WHERE workspace_id = @workspace_id AND id = @id;

-- name: GetTaskForUpdate :one
SELECT * FROM tasks WHERE workspace_id = @workspace_id AND id = @id FOR UPDATE;

-- name: UpdateTask :one
UPDATE tasks
SET queue_id = sqlc.narg(queue_id), title = @title, body = @body, status = @status, priority = @priority,
    assignee_id = sqlc.narg(assignee_id), closed_at = sqlc.narg(closed_at), updated_at = @updated_at,
    version = version + 1
WHERE workspace_id = @workspace_id AND id = @id
RETURNING *;

-- name: DeleteTask :exec
DELETE FROM tasks WHERE workspace_id = @workspace_id AND id = @id;

-- name: ListTasks :many
-- A page of tasks, newest first, before the number before_number (0: from
-- the newest). open_only and closed_only pick the state; assignee_id and
-- queue_id filter when set; unassigned and no_queue ask for NULL.
SELECT * FROM tasks
WHERE workspace_id = @workspace_id
  AND (@before_number::bigint = 0 OR number < @before_number::bigint)
  AND (NOT @open_only::boolean OR closed_at IS NULL)
  AND (NOT @closed_only::boolean OR closed_at IS NOT NULL)
  AND (sqlc.narg(assignee_id)::uuid IS NULL OR assignee_id = sqlc.narg(assignee_id)::uuid)
  AND (NOT @unassigned::boolean OR assignee_id IS NULL)
  AND (sqlc.narg(queue_id)::uuid IS NULL OR queue_id = sqlc.narg(queue_id)::uuid)
  AND (NOT @no_queue::boolean OR queue_id IS NULL)
  AND (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text)
  AND (@query::text = '' OR title ILIKE '%' || @query::text || '%' OR number::text = @query::text)
ORDER BY number DESC
LIMIT @lim;

-- name: CountOpenAssigned :one
-- The open tasks of a member, for the counter in the menu.
SELECT count(*) FROM tasks
WHERE workspace_id = @workspace_id AND assignee_id = @assignee_id AND closed_at IS NULL;

-- name: ListQueues :many
SELECT q.id, q.name, q.created_at,
       coalesce((SELECT array_agg(m.user_id ORDER BY m.user_id) FROM task_queue_members m
                 WHERE m.workspace_id = q.workspace_id AND m.queue_id = q.id), '{}')::uuid[] AS member_ids,
       (SELECT count(*) FROM tasks t WHERE t.workspace_id = q.workspace_id AND t.queue_id = q.id AND t.closed_at IS NULL) AS open_tasks
FROM task_queues q
WHERE q.workspace_id = @workspace_id
ORDER BY lower(q.name), q.id;

-- name: GetQueue :one
SELECT * FROM task_queues WHERE workspace_id = @workspace_id AND id = @id;

-- name: CountQueues :one
SELECT count(*) FROM task_queues WHERE workspace_id = @workspace_id;

-- name: InsertQueue :one
INSERT INTO task_queues (id, workspace_id, name, created_at)
VALUES (@id, @workspace_id, @name, @created_at)
RETURNING *;

-- name: RenameQueue :one
UPDATE task_queues SET name = @name
WHERE workspace_id = @workspace_id AND id = @id
RETURNING *;

-- name: DeleteQueue :execrows
DELETE FROM task_queues WHERE workspace_id = @workspace_id AND id = @id;

-- name: ClearQueueMembers :exec
DELETE FROM task_queue_members WHERE workspace_id = @workspace_id AND queue_id = @queue_id;

-- name: AddQueueMember :exec
INSERT INTO task_queue_members (workspace_id, queue_id, user_id)
VALUES (@workspace_id, @queue_id, @user_id)
ON CONFLICT DO NOTHING;

-- name: QueueMemberIDs :many
SELECT user_id FROM task_queue_members
WHERE workspace_id = @workspace_id AND queue_id = @queue_id
ORDER BY user_id;

-- name: InsertActivity :exec
INSERT INTO task_activity (id, workspace_id, task_id, actor_id, kind, body, meta, created_at)
VALUES (@id, @workspace_id, @task_id, sqlc.narg(actor_id), @kind, sqlc.narg(body), @meta, @created_at);

-- name: ListActivity :many
-- The latest 500 lines of the feed, oldest first.
SELECT a.* FROM (
    SELECT * FROM task_activity
    WHERE workspace_id = @workspace_id AND task_id = @task_id
    ORDER BY id DESC
    LIMIT 500
) a
ORDER BY a.id;

-- name: LinkConversation :execrows
INSERT INTO task_conversations (workspace_id, task_id, conversation_id, created_by, created_at)
VALUES (@workspace_id, @task_id, @conversation_id, sqlc.narg(created_by), @created_at)
ON CONFLICT DO NOTHING;

-- name: UnlinkConversation :execrows
DELETE FROM task_conversations
WHERE workspace_id = @workspace_id AND task_id = @task_id AND conversation_id = @conversation_id;

-- name: TaskConversationIDs :many
SELECT conversation_id FROM task_conversations
WHERE workspace_id = @workspace_id AND task_id = @task_id
ORDER BY conversation_id; -- one order for every task: no deadlock between two tasks sharing conversations

-- name: ConversationTasks :many
-- The tasks of a conversation, oldest link first.
SELECT t.* FROM tasks t
JOIN task_conversations l ON l.workspace_id = t.workspace_id AND l.task_id = t.id
WHERE l.workspace_id = @workspace_id AND l.conversation_id = @conversation_id
ORDER BY l.created_at, t.id;

-- name: HasOpenTask :one
-- Whether a task of the conversation is open: it waits for the fix.
SELECT EXISTS (
    SELECT 1 FROM tasks t
    JOIN task_conversations l ON l.workspace_id = t.workspace_id AND l.task_id = t.id
    WHERE l.workspace_id = @workspace_id AND l.conversation_id = @conversation_id AND t.closed_at IS NULL
);

-- name: DoneTask :many
-- The task of the conversation done most recently: the reply is drafted
-- from it.
SELECT t.* FROM tasks t
JOIN task_conversations l ON l.workspace_id = t.workspace_id AND l.task_id = t.id
WHERE l.workspace_id = @workspace_id AND l.conversation_id = @conversation_id AND t.status = 'done'
ORDER BY t.closed_at DESC, t.id DESC
LIMIT 1;

-- name: GetSettings :one
SELECT * FROM task_settings WHERE workspace_id = @workspace_id;

-- name: UpsertSettings :one
INSERT INTO task_settings (workspace_id, enabled, conversation_button, reply_ru, reply_en, updated_at)
VALUES (@workspace_id, @enabled, @conversation_button, sqlc.narg(reply_ru), sqlc.narg(reply_en), @updated_at)
ON CONFLICT (workspace_id) DO UPDATE
SET enabled = EXCLUDED.enabled, conversation_button = EXCLUDED.conversation_button,
    reply_ru = EXCLUDED.reply_ru, reply_en = EXCLUDED.reply_en, updated_at = EXCLUDED.updated_at
RETURNING *;
