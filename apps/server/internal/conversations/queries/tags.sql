-- name: ListTags :many
-- Every tag of the workspace by name; the number is bounded by MaxTags.
SELECT * FROM tags
WHERE workspace_id = @workspace_id
ORDER BY name, id
LIMIT @lim;

-- name: CountTags :one
SELECT count(*) FROM tags WHERE workspace_id = @workspace_id;

-- name: CreateTag :one
-- No row when the name is taken (case-insensitive).
INSERT INTO tags (id, workspace_id, name, color, created_by)
VALUES (@id, @workspace_id, @name, @color, @created_by)
ON CONFLICT (workspace_id, name) DO NOTHING
RETURNING *;

-- name: CountTagsByID :one
SELECT count(*) FROM tags
WHERE workspace_id = @workspace_id AND id = ANY(@ids::uuid[]);

-- name: ConversationTagIDs :many
SELECT tag_id FROM conversation_tags
WHERE workspace_id = @workspace_id AND conversation_id = @conversation_id
ORDER BY created_at, tag_id;

-- name: AddConversationTags :execrows
INSERT INTO conversation_tags (workspace_id, conversation_id, tag_id, created_by)
SELECT @workspace_id, @conversation_id, t, @created_by
FROM unnest(@tag_ids::uuid[]) AS t
ON CONFLICT DO NOTHING;

-- name: RemoveConversationTags :execrows
-- Removes every tag of the conversation not in keep.
DELETE FROM conversation_tags
WHERE workspace_id = @workspace_id AND conversation_id = @conversation_id
  AND NOT (tag_id = ANY(@keep::uuid[]));
