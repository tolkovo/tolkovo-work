-- name: NextTaskNumber :one
INSERT INTO task_counters (workspace_id, last_number)
VALUES (@workspace_id, 1)
ON CONFLICT (workspace_id) DO UPDATE SET last_number = task_counters.last_number + 1
RETURNING last_number;

-- name: InsertTask :one
INSERT INTO tasks (id, workspace_id, number, queue_id, topic_id, title, body, status, priority, assignee_id, created_by, created_at, updated_at, status_changed_at)
VALUES (@id, @workspace_id, @number, sqlc.narg(queue_id), sqlc.narg(topic_id), @title, @body, 'open', @priority, sqlc.narg(assignee_id), sqlc.narg(created_by), @created_at, @created_at, @created_at)
RETURNING *;

-- name: GetTask :one
SELECT * FROM tasks WHERE workspace_id = @workspace_id AND id = @id;

-- name: TaskIDByNumber :one
SELECT id FROM tasks WHERE workspace_id = @workspace_id AND number = @number;

-- name: GetTaskForUpdate :one
SELECT * FROM tasks WHERE workspace_id = @workspace_id AND id = @id FOR UPDATE;

-- name: UpdateTask :one
-- status_changed_at moves only when the status does: the board counts the
-- time in a column from it.
UPDATE tasks
SET queue_id = sqlc.narg(queue_id), topic_id = sqlc.narg(topic_id), title = @title, body = @body, status = @status, priority = @priority,
    assignee_id = sqlc.narg(assignee_id), closed_at = sqlc.narg(closed_at), updated_at = @updated_at,
    blocked_reason = sqlc.narg(blocked_reason),
    status_changed_at = CASE WHEN status = @status THEN status_changed_at ELSE @updated_at END,
    version = version + 1
WHERE workspace_id = @workspace_id AND id = @id
RETURNING *;

-- name: DeleteTask :exec
DELETE FROM tasks WHERE workspace_id = @workspace_id AND id = @id;

-- name: ListTasks :many
-- A page of tasks, newest first, before the number before_number (0: from
-- the newest). open_only and closed_only pick the state; assignee_id and
-- queue_id and topic_id filter when set; unassigned, no_queue and no_topic
-- ask for NULL; tag_id keeps the tasks with that tag.
SELECT t.* FROM tasks t
WHERE t.workspace_id = @workspace_id
  AND (@before_number::bigint = 0 OR t.number < @before_number::bigint)
  AND (NOT @open_only::boolean OR t.closed_at IS NULL)
  AND (NOT @closed_only::boolean OR t.closed_at IS NOT NULL)
  AND (sqlc.narg(assignee_id)::uuid IS NULL OR t.assignee_id = sqlc.narg(assignee_id)::uuid)
  AND (NOT @unassigned::boolean OR t.assignee_id IS NULL)
  AND (sqlc.narg(queue_id)::uuid IS NULL OR t.queue_id = sqlc.narg(queue_id)::uuid)
  AND (NOT @no_queue::boolean OR t.queue_id IS NULL)
  AND (sqlc.narg(topic_id)::uuid IS NULL OR t.topic_id = sqlc.narg(topic_id)::uuid)
  AND (NOT @no_topic::boolean OR t.topic_id IS NULL)
  AND (sqlc.narg(blocked)::boolean IS NULL OR (t.blocked_reason IS NOT NULL) = sqlc.narg(blocked)::boolean)
  AND (sqlc.narg(status)::text IS NULL OR t.status = sqlc.narg(status)::text)
  AND (sqlc.narg(tag_id)::uuid IS NULL OR EXISTS (
       SELECT 1 FROM task_tag_links l
       WHERE l.workspace_id = t.workspace_id AND l.task_id = t.id AND l.tag_id = sqlc.narg(tag_id)::uuid))
  AND (@query::text = '' OR t.title ILIKE '%' || @query::text || '%' OR t.number::text = @query::text)
ORDER BY t.number DESC
LIMIT @lim;

-- name: BoardTasks :many
-- The board (ADR-0019): the first per_column tasks of every status in the
-- order of its column, with the size of the column. An open column puts the
-- most urgent first, then the one standing longest in it; done and canceled
-- hold the tasks closed since closed_since, the latest first, and canceled
-- only when asked. The filters are those of ListTasks.
WITH ranked AS (
    SELECT t.id,
           count(*) OVER (PARTITION BY t.status) AS column_count,
           row_number() OVER (PARTITION BY t.status ORDER BY
               CASE WHEN t.closed_at IS NULL THEN CASE t.priority WHEN 'urgent' THEN 0 WHEN 'high' THEN 1 WHEN 'normal' THEN 2 ELSE 3 END END,
               CASE WHEN t.closed_at IS NULL THEN t.status_changed_at END,
               CASE WHEN t.closed_at IS NULL THEN t.number END,
               t.status_changed_at DESC, t.number DESC) AS pos
    FROM tasks t
    WHERE t.workspace_id = @workspace_id
      AND (t.closed_at IS NULL OR (t.status_changed_at >= @closed_since::timestamptz AND (t.status = 'done' OR @canceled::boolean)))
      AND (sqlc.narg(assignee_id)::uuid IS NULL OR t.assignee_id = sqlc.narg(assignee_id)::uuid)
      AND (NOT @unassigned::boolean OR t.assignee_id IS NULL)
      AND (sqlc.narg(queue_id)::uuid IS NULL OR t.queue_id = sqlc.narg(queue_id)::uuid)
      AND (NOT @no_queue::boolean OR t.queue_id IS NULL)
      AND (sqlc.narg(topic_id)::uuid IS NULL OR t.topic_id = sqlc.narg(topic_id)::uuid)
      AND (NOT @no_topic::boolean OR t.topic_id IS NULL)
      AND (sqlc.narg(blocked)::boolean IS NULL OR (t.blocked_reason IS NOT NULL) = sqlc.narg(blocked)::boolean)
      AND (sqlc.narg(tag_id)::uuid IS NULL OR EXISTS (
           SELECT 1 FROM task_tag_links l
           WHERE l.workspace_id = t.workspace_id AND l.task_id = t.id AND l.tag_id = sqlc.narg(tag_id)::uuid))
      AND (@query::text = '' OR t.title ILIKE '%' || @query::text || '%' OR t.number::text = @query::text)
)
SELECT sqlc.embed(t), r.column_count
FROM ranked r
JOIN tasks t ON t.workspace_id = @workspace_id AND t.id = r.id
WHERE r.pos <= @per_column::bigint
ORDER BY t.status, r.pos;

-- name: ListTasksFlow :many
-- A page of one column of the board (BoardTasks order) after the task at
-- (after_rank, after_changed, after_number); after_number 0 starts from the
-- top. closed_order is the order of done and canceled, latest first;
-- closed_since, when set, keeps the tasks closed since then.
SELECT t.* FROM tasks t
WHERE t.workspace_id = @workspace_id AND t.status = @status
  AND (sqlc.narg(closed_since)::timestamptz IS NULL OR t.closed_at IS NULL OR t.status_changed_at >= sqlc.narg(closed_since)::timestamptz)
  AND (@after_number::bigint = 0 OR CASE WHEN @closed_order::boolean
       THEN (t.status_changed_at, t.number) < (@after_changed::timestamptz, @after_number::bigint)
       ELSE (CASE t.priority WHEN 'urgent' THEN 0 WHEN 'high' THEN 1 WHEN 'normal' THEN 2 ELSE 3 END, t.status_changed_at, t.number)
            > (@after_rank::int, @after_changed::timestamptz, @after_number::bigint) END)
  AND (sqlc.narg(assignee_id)::uuid IS NULL OR t.assignee_id = sqlc.narg(assignee_id)::uuid)
  AND (NOT @unassigned::boolean OR t.assignee_id IS NULL)
  AND (sqlc.narg(queue_id)::uuid IS NULL OR t.queue_id = sqlc.narg(queue_id)::uuid)
  AND (NOT @no_queue::boolean OR t.queue_id IS NULL)
  AND (sqlc.narg(topic_id)::uuid IS NULL OR t.topic_id = sqlc.narg(topic_id)::uuid)
  AND (NOT @no_topic::boolean OR t.topic_id IS NULL)
  AND (sqlc.narg(blocked)::boolean IS NULL OR (t.blocked_reason IS NOT NULL) = sqlc.narg(blocked)::boolean)
  AND (sqlc.narg(tag_id)::uuid IS NULL OR EXISTS (
       SELECT 1 FROM task_tag_links l
       WHERE l.workspace_id = t.workspace_id AND l.task_id = t.id AND l.tag_id = sqlc.narg(tag_id)::uuid))
  AND (@query::text = '' OR t.title ILIKE '%' || @query::text || '%' OR t.number::text = @query::text)
ORDER BY
  CASE WHEN NOT @closed_order::boolean THEN CASE t.priority WHEN 'urgent' THEN 0 WHEN 'high' THEN 1 WHEN 'normal' THEN 2 ELSE 3 END END,
  CASE WHEN NOT @closed_order::boolean THEN t.status_changed_at END,
  CASE WHEN NOT @closed_order::boolean THEN t.number END,
  t.status_changed_at DESC, t.number DESC
LIMIT @lim;

-- name: ConversationCounts :many
-- The number of linked conversations of a page of tasks in one query.
SELECT task_id, count(*) AS n FROM task_conversations
WHERE workspace_id = @workspace_id AND task_id = ANY(@task_ids::uuid[])
GROUP BY task_id;

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

-- Files of tasks (migration 00033).

-- name: InsertTaskFile :one
INSERT INTO task_files (id, workspace_id, storage_key, file_name, content_type, size, uploaded_by, created_at)
VALUES (@id, @workspace_id, @storage_key, @file_name, @content_type, @size, @uploaded_by, @created_at)
RETURNING *;

-- name: LooseTaskFiles :many
-- Files of the member not taken yet, among ids.
SELECT * FROM task_files
WHERE workspace_id = @workspace_id AND id = ANY(@ids::uuid[]) AND uploaded_by = @uploaded_by
  AND task_id IS NULL AND attached_at IS NULL
ORDER BY id;

-- name: AttachTaskFiles :execrows
UPDATE task_files SET task_id = @task_id, activity_id = sqlc.narg(activity_id), attached_at = @attached_at
WHERE workspace_id = @workspace_id AND id = ANY(@ids::uuid[]) AND uploaded_by = @uploaded_by
  AND task_id IS NULL AND attached_at IS NULL;

-- name: TaskFiles :many
-- The files of a task, in the order they came.
SELECT * FROM task_files
WHERE workspace_id = @workspace_id AND task_id = @task_id
ORDER BY created_at, id;

-- name: CountTaskFiles :one
SELECT count(*) FROM task_files WHERE workspace_id = @workspace_id AND task_id = @task_id;

-- name: GetTaskFile :one
SELECT * FROM task_files WHERE workspace_id = @workspace_id AND id = @id;

-- name: DetachTaskFile :execrows
-- Removes a file from a task's description; the cleanup deletes it.
UPDATE task_files SET task_id = NULL
WHERE workspace_id = @workspace_id AND id = @id AND task_id = @task_id AND activity_id IS NULL;

-- name: DeleteTaskFile :exec
DELETE FROM task_files WHERE workspace_id = @workspace_id AND id = @id;

-- name: LooseTaskFilesToClean :many
-- Runs as tolkovo_system (policy system_see_loose): files a task gave up,
-- and files nobody took within the upload's lifetime.
SELECT id, workspace_id FROM task_files
WHERE task_id IS NULL AND (attached_at IS NOT NULL OR created_at < @before)
ORDER BY created_at
LIMIT @lim;

-- Tags of tasks (migration 00037).

-- name: ListTaskTags :many
-- Every tag of the workspace by name; the number is bounded by MaxTags.
SELECT * FROM task_tags
WHERE workspace_id = @workspace_id
ORDER BY lower(name), id;

-- name: CountTaskTags :one
SELECT count(*) FROM task_tags WHERE workspace_id = @workspace_id;

-- name: InsertTaskTag :one
INSERT INTO task_tags (id, workspace_id, name, color, created_by, created_at)
VALUES (@id, @workspace_id, @name, @color, sqlc.narg(created_by), @created_at)
RETURNING *;

-- name: UpdateTaskTag :one
UPDATE task_tags SET name = @name, color = @color
WHERE workspace_id = @workspace_id AND id = @id
RETURNING *;

-- name: DeleteTaskTag :execrows
DELETE FROM task_tags WHERE workspace_id = @workspace_id AND id = @id;

-- name: CountTaskTagsByID :one
SELECT count(*) FROM task_tags WHERE workspace_id = @workspace_id AND id = ANY(@ids::uuid[]);

-- name: TagsOfTasks :many
-- The tags of a page of tasks in one query, in the order they were put on.
SELECT task_id, tag_id FROM task_tag_links
WHERE workspace_id = @workspace_id AND task_id = ANY(@task_ids::uuid[])
ORDER BY task_id, created_at, tag_id;

-- name: AddTaskTags :many
-- Puts the tags on the task; returns the ones it did not have.
INSERT INTO task_tag_links (workspace_id, task_id, tag_id, created_at)
SELECT @workspace_id, @task_id, t, @created_at
FROM unnest(@tag_ids::uuid[]) AS t
ON CONFLICT DO NOTHING
RETURNING tag_id;

-- name: RemoveTaskTags :many
-- Takes every tag of the task not in keep off it; returns them.
DELETE FROM task_tag_links
WHERE workspace_id = @workspace_id AND task_id = @task_id
  AND NOT (tag_id = ANY(@keep::uuid[]))
RETURNING tag_id;

-- name: TopicQueue :one
-- A topic of the workspace and the queue of its tasks (topics belongs to
-- conversations; tasks only reads it). No row: no such topic.
SELECT id, task_queue_id FROM topics WHERE workspace_id = @workspace_id AND id = @id;

-- name: ConversationTopic :one
-- The topic of a conversation a task is created from.
SELECT topic_id FROM conversations WHERE workspace_id = @workspace_id AND id = @id;

-- name: ListColumnRules :many
-- The rules of the board's columns: the workspace's (queue_id NULL) and the
-- queues'.
SELECT * FROM task_column_rules
WHERE workspace_id = @workspace_id
ORDER BY queue_id NULLS FIRST, status;

-- name: DeleteColumnRules :exec
DELETE FROM task_column_rules WHERE workspace_id = @workspace_id;

-- name: InsertColumnRule :exec
INSERT INTO task_column_rules (workspace_id, queue_id, status, wip_limit, age_days)
VALUES (@workspace_id, sqlc.narg(queue_id), @status, sqlc.narg(wip_limit), sqlc.narg(age_days));

-- name: LockOpenTasksOf :many
-- The open tasks of a member who leaves the team (ADR-0020).
SELECT * FROM tasks
WHERE workspace_id = @workspace_id AND assignee_id = @user_id AND closed_at IS NULL
ORDER BY number
FOR UPDATE;

-- name: SetTaskAssignee :one
UPDATE tasks
SET assignee_id = sqlc.narg(assignee_id), updated_at = @updated_at, version = version + 1
WHERE workspace_id = @workspace_id AND id = @id
RETURNING *;

-- name: RemoveQueueMember :exec
-- A member who left the team is told about no queue.
DELETE FROM task_queue_members WHERE workspace_id = @workspace_id AND user_id = @user_id;
