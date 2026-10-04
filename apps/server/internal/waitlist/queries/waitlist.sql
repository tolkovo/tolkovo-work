-- waitlist_entries is a global table without RLS (migration 00005).

-- name: UpsertPending :one
-- A new address becomes a pending entry; a pending one takes the latest
-- answers. A confirmed entry is left alone and no row is returned, so an
-- anonymous request cannot change someone's confirmed subscription.
INSERT INTO waitlist_entries (id, email, locale, channels, consent_version, created_at, updated_at)
VALUES (@id, @email, @locale, @channels::text[], @consent_version, @now, @now)
ON CONFLICT (email) DO UPDATE
SET locale = EXCLUDED.locale,
    channels = EXCLUDED.channels,
    consent_version = EXCLUDED.consent_version,
    updated_at = EXCLUDED.updated_at
WHERE waitlist_entries.status = 'pending'
RETURNING id, send_count;

-- name: IssueTokens :one
-- Stores fresh token hashes of a pending entry that may get another email
-- and returns where to send it. No row: confirmed, deleted or out of sends.
UPDATE waitlist_entries
SET confirm_token_hash = @confirm_hash,
    confirm_expires_at = sqlc.arg(expires_at)::timestamptz,
    unsubscribe_token_hash = @unsubscribe_hash,
    send_count = send_count + 1,
    last_sent_at = sqlc.arg(now)::timestamptz
WHERE id = @id AND status = 'pending' AND send_count < @max_sends
RETURNING email, locale;

-- name: Confirm :one
-- Confirming twice with the same link within its lifetime is fine.
UPDATE waitlist_entries
SET status = 'confirmed',
    confirmed_at = COALESCE(confirmed_at, @now),
    updated_at = @now
WHERE confirm_token_hash = @token_hash AND confirm_expires_at > @now
RETURNING id;

-- name: DeleteByUnsubscribeToken :execrows
DELETE FROM waitlist_entries
WHERE unsubscribe_token_hash = @token_hash;

-- name: PurgePending :execrows
DELETE FROM waitlist_entries
WHERE id IN (
    SELECT p.id FROM waitlist_entries AS p
    WHERE p.status = 'pending' AND p.updated_at < sqlc.arg(before)::timestamptz
    LIMIT sqlc.arg(max_rows)
);

-- name: ClearExpiredConfirmTokens :execrows
UPDATE waitlist_entries
SET confirm_token_hash = NULL, confirm_expires_at = NULL
WHERE id IN (
    SELECT p.id FROM waitlist_entries AS p
    WHERE p.confirm_expires_at < sqlc.arg(now)::timestamptz
    LIMIT sqlc.arg(max_rows)
);
