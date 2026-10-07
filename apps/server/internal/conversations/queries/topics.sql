-- name: ListTopics :many
-- Every topic of the workspace in the order of the buttons, with its
-- members; the number is bounded by MaxTopics.
SELECT sqlc.embed(t),
       coalesce((SELECT array_agg(m.user_id ORDER BY m.user_id) FROM topic_members m
                 WHERE m.workspace_id = t.workspace_id AND m.topic_id = t.id), '{}')::uuid[] AS member_ids
FROM topics t
WHERE t.workspace_id = @workspace_id
ORDER BY t.position, t.name, t.id
LIMIT @lim;

-- name: GetTopic :one
SELECT * FROM topics WHERE workspace_id = @workspace_id AND id = @id;

-- name: CountTopics :one
SELECT count(*) FROM topics WHERE workspace_id = @workspace_id;

-- name: CreateTopic :one
-- No row when the name is taken (case-insensitive). A new topic goes last.
INSERT INTO topics (id, workspace_id, name, color, position, priority, task_queue_id, created_at, updated_at)
VALUES (@id, @workspace_id, @name, @color,
        (SELECT coalesce(max(position) + 1, 0) FROM topics WHERE workspace_id = @workspace_id),
        @priority, sqlc.narg(task_queue_id), @now, @now)
ON CONFLICT (workspace_id, name) DO NOTHING
RETURNING *;

-- name: UpdateTopic :one
UPDATE topics
SET name = @name, color = @color, priority = @priority, task_queue_id = sqlc.narg(task_queue_id), updated_at = @now
WHERE workspace_id = @workspace_id AND id = @id
RETURNING *;

-- name: SetTopicPosition :execrows
UPDATE topics SET position = @position
WHERE workspace_id = @workspace_id AND id = @id;

-- name: DeleteTopic :execrows
DELETE FROM topics WHERE workspace_id = @workspace_id AND id = @id;

-- name: ClearTopicMembers :exec
DELETE FROM topic_members WHERE workspace_id = @workspace_id AND topic_id = @topic_id;

-- name: AddTopicMembers :execrows
-- Only active members of the workspace are added.
INSERT INTO topic_members (workspace_id, topic_id, user_id)
SELECT m.workspace_id, @topic_id, m.user_id
FROM memberships m
WHERE m.workspace_id = @workspace_id AND m.user_id = ANY(@user_ids::uuid[]) AND m.is_active
ON CONFLICT DO NOTHING;

-- name: TopicMemberIDs :many
SELECT user_id FROM topic_members
WHERE workspace_id = @workspace_id AND topic_id = @topic_id;

-- name: TopicMembersOf :many
-- The members of the given topics, for the router.
SELECT topic_id, user_id FROM topic_members
WHERE workspace_id = @workspace_id AND topic_id = ANY(@topic_ids::uuid[]);

-- name: SetConversationTopic :exec
-- A topic comes with its priority; clients compare versions.
UPDATE conversations
SET topic_id = sqlc.narg(topic_id), priority = @priority, version = version + 1, updated_at = now()
WHERE workspace_id = @workspace_id AND id = @id;

-- name: MarkTopicAsked :exec
UPDATE conversations SET topic_asked_at = @at
WHERE workspace_id = @workspace_id AND id = @id;

-- name: WorkspaceLocale :one
-- The language of the words a channel puts around the topics
-- (workspaces belongs to tenancy; read only).
SELECT locale FROM workspaces WHERE id = @workspace_id;
