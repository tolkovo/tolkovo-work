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
