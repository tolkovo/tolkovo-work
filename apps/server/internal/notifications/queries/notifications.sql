-- name: GetPrefs :one
SELECT email_missed, email_delay_minutes, tg_assigned, tg_messages, tg_queue
FROM notification_prefs
WHERE workspace_id = $1 AND user_id = $2;

-- Fields left NULL keep their stored value, or the default of a new row.
-- name: UpsertPrefs :one
INSERT INTO notification_prefs (workspace_id, user_id, email_missed, email_delay_minutes, tg_assigned, tg_messages, tg_queue, updated_at)
VALUES (
    @workspace_id, @user_id,
    COALESCE(sqlc.narg(email_missed)::boolean, true),
    COALESCE(sqlc.narg(email_delay_minutes)::smallint, 15),
    COALESCE(sqlc.narg(tg_assigned)::boolean, true),
    COALESCE(sqlc.narg(tg_messages)::boolean, true),
    COALESCE(sqlc.narg(tg_queue)::boolean, true),
    @updated_at
)
ON CONFLICT (workspace_id, user_id) DO UPDATE
SET email_missed = COALESCE(sqlc.narg(email_missed)::boolean, notification_prefs.email_missed),
    email_delay_minutes = COALESCE(sqlc.narg(email_delay_minutes)::smallint, notification_prefs.email_delay_minutes),
    tg_assigned = COALESCE(sqlc.narg(tg_assigned)::boolean, notification_prefs.tg_assigned),
    tg_messages = COALESCE(sqlc.narg(tg_messages)::boolean, notification_prefs.tg_messages),
    tg_queue = COALESCE(sqlc.narg(tg_queue)::boolean, notification_prefs.tg_queue),
    updated_at = EXCLUDED.updated_at
RETURNING email_missed, email_delay_minutes, tg_assigned, tg_messages, tg_queue;

-- name: GetMissedMark :one
SELECT covered_up_to
FROM missed_email_marks
WHERE workspace_id = $1 AND conversation_id = $2 AND user_id = $3;

-- name: SetMissedMark :exec
INSERT INTO missed_email_marks (workspace_id, conversation_id, user_id, covered_up_to, sent_at)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (workspace_id, conversation_id, user_id) DO UPDATE
SET covered_up_to = GREATEST(missed_email_marks.covered_up_to, EXCLUDED.covered_up_to),
    sent_at = EXCLUDED.sent_at;
