-- name: GetNotifyBot :one
SELECT * FROM notify_bots WHERE workspace_id = @workspace_id;

-- name: UpsertNotifyBot :one
-- A new bot replaces the old one; its offset starts again.
INSERT INTO notify_bots (workspace_id, id, bot_id, username, sealed, updates_offset, created_by, created_at)
VALUES (@workspace_id, @id, @bot_id, @username, @sealed, 0, @created_by, @created_at)
ON CONFLICT (workspace_id) DO UPDATE
SET id = EXCLUDED.id, bot_id = EXCLUDED.bot_id, username = EXCLUDED.username, sealed = EXCLUDED.sealed,
    updates_offset = 0, created_by = EXCLUDED.created_by, created_at = EXCLUDED.created_at
RETURNING *;

-- name: DeleteNotifyBot :execrows
DELETE FROM notify_bots WHERE workspace_id = @workspace_id;

-- name: AdvanceNotifyOffset :exec
UPDATE notify_bots SET updates_offset = GREATEST(updates_offset, @updates_offset)
WHERE workspace_id = @workspace_id AND id = @id;

-- name: ClearTelegramLinks :exec
-- The chats belong to the bot that is gone: every member links again.
DELETE FROM telegram_links WHERE workspace_id = @workspace_id;

-- name: GetTelegramLink :one
SELECT * FROM telegram_links WHERE workspace_id = @workspace_id AND user_id = @user_id;

-- name: StartTelegramLink :exec
-- A new code replaces the one before; a linked chat stays until Start.
INSERT INTO telegram_links (workspace_id, user_id, code_hash, code_expires_at)
VALUES (@workspace_id, @user_id, @code_hash, @code_expires_at)
ON CONFLICT (workspace_id, user_id) DO UPDATE
SET code_hash = EXCLUDED.code_hash, code_expires_at = EXCLUDED.code_expires_at;

-- name: CompleteTelegramLink :execrows
-- Start with a live code: the member's chat.
UPDATE telegram_links
SET chat_id = @chat_id, telegram_name = @telegram_name, linked_at = @linked_at, code_hash = NULL, code_expires_at = NULL
WHERE workspace_id = @workspace_id AND code_hash = @code_hash AND code_expires_at > @linked_at;

-- name: ChatLinkedToOther :one
-- Whether the chat is linked to a member other than the one whose live
-- code it brings: one Telegram gets the messages of one member.
SELECT EXISTS (
    SELECT 1 FROM telegram_links o
    JOIN telegram_links c ON c.workspace_id = o.workspace_id AND c.user_id <> o.user_id
    WHERE o.workspace_id = @workspace_id AND o.chat_id = @chat_id::bigint
      AND c.code_hash = @code_hash AND c.code_expires_at > @now::timestamptz
);

-- name: DeleteTelegramLink :exec
DELETE FROM telegram_links WHERE workspace_id = @workspace_id AND user_id = @user_id;

-- name: LinkedChats :many
-- The chats of the members among user_ids who linked their Telegram.
SELECT user_id, chat_id::bigint AS chat_id FROM telegram_links
WHERE workspace_id = @workspace_id AND user_id = ANY(@user_ids::uuid[]) AND chat_id IS NOT NULL;

-- name: UnlinkChat :exec
-- The member blocked the bot or left Telegram.
UPDATE telegram_links SET chat_id = NULL, telegram_name = NULL, linked_at = NULL
WHERE workspace_id = @workspace_id AND user_id = @user_id AND chat_id = @chat_id;

-- name: ChatWatchers :many
-- Who hears about conversations in Telegram (docs/06 I-06): active members
-- among user_ids (all when empty) whose role answers customers (answering),
-- who linked their Telegram and did not turn off the kind (want: assigned,
-- messages or queue), of the line, or of the topic when it has members.
-- memberships, roles, line_members, topic_members and notification_prefs
-- are read here, as
-- LinkedChats reads telegram_links of members.
SELECT l.user_id, l.chat_id::bigint AS chat_id
FROM telegram_links l
JOIN memberships m ON m.workspace_id = l.workspace_id AND m.user_id = l.user_id
JOIN roles r ON r.id = m.role_id
LEFT JOIN notification_prefs p ON p.workspace_id = l.workspace_id AND p.user_id = l.user_id
WHERE l.workspace_id = @workspace_id AND l.chat_id IS NOT NULL AND m.is_active
  AND r.key = ANY(@answering::text[])
  AND (cardinality(@user_ids::uuid[]) = 0 OR l.user_id = ANY(@user_ids::uuid[]))
  -- A line goes to its members alone (ADR-0021); on the first line a
  -- topic with members goes to them alone (docs/02 §7.3).
  AND (sqlc.narg(line_id)::uuid IS NULL
       OR EXISTS (SELECT 1 FROM line_members lm WHERE lm.workspace_id = l.workspace_id AND lm.line_id = sqlc.narg(line_id)::uuid
                  AND lm.user_id = l.user_id))
  AND (sqlc.narg(line_id)::uuid IS NOT NULL OR sqlc.narg(topic_id)::uuid IS NULL
       OR NOT EXISTS (SELECT 1 FROM topic_members tm WHERE tm.workspace_id = l.workspace_id AND tm.topic_id = sqlc.narg(topic_id)::uuid)
       OR EXISTS (SELECT 1 FROM topic_members tm WHERE tm.workspace_id = l.workspace_id AND tm.topic_id = sqlc.narg(topic_id)::uuid
                  AND tm.user_id = l.user_id))
  AND CASE @want::text
        WHEN 'assigned' THEN coalesce(p.tg_assigned, true)
        WHEN 'messages' THEN coalesce(p.tg_messages, true)
        ELSE coalesce(p.tg_queue, true)
      END
ORDER BY l.user_id;

-- name: ChannelName :one
-- The name and type of a conversation's channel for the message text.
-- channels belongs to the channels module; this read-only lookup runs in
-- the job.
SELECT type, name FROM channels WHERE workspace_id = @workspace_id AND id = @id;
