-- name: InsertUpload :one
INSERT INTO widget_uploads (
    id, workspace_id, channel_id, session_id, storage_key, file_name, content_type, size, created_at
) VALUES (
    @id, @workspace_id, @channel_id, @session_id, @storage_key, @file_name, @content_type, @size, @now
)
RETURNING *;

-- name: SendableUploads :many
-- The session's uploads a message may take: unsent, or taken by a message
-- with this key (a retry), and younger than the cleanup.
SELECT * FROM widget_uploads
WHERE workspace_id = @workspace_id AND session_id = @session_id AND id = ANY(@ids::uuid[])
  AND (message_key IS NULL OR message_key = @message_key)
  AND created_at > @since;

-- name: ClaimUploads :execrows
-- A message takes its uploads. A concurrent message with another key that
-- took one first leaves fewer rows than ids.
UPDATE widget_uploads
SET message_key = @message_key
WHERE workspace_id = @workspace_id AND session_id = @session_id AND id = ANY(@ids::uuid[])
  AND (message_key IS NULL OR message_key = @message_key)
  AND created_at > @since;

-- name: DeleteUpload :exec
DELETE FROM widget_uploads
WHERE workspace_id = @workspace_id AND id = @id;

-- name: OldUploads :many
-- The worker's cleanup as tolkovo_system: the policy shows uploads older
-- than an hour, ids only.
SELECT id, workspace_id FROM widget_uploads
WHERE created_at < @before
ORDER BY created_at
LIMIT @lim;

-- name: GetUpload :one
SELECT * FROM widget_uploads
WHERE workspace_id = @workspace_id AND id = @id;
