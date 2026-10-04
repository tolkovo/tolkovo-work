-- name: InsertEvent :exec
INSERT INTO outbox_events (workspace_id, topic, payload)
VALUES ($1, $2, $3);

-- name: UnpublishedEvents :many
SELECT id, workspace_id, topic, payload, created_at
FROM outbox_events
WHERE published_at IS NULL
ORDER BY id
LIMIT $1;

-- name: ReserveSeq :one
-- ReserveSeq reserves n consecutive seq values and returns the last one.
-- Only the relay leader calls it, so nextval and setval do not race.
SELECT setval('outbox_events_seq', nextval('outbox_events_seq') + sqlc.arg(n)::bigint - 1)::bigint AS last_seq;

-- name: MarkPublished :execrows
UPDATE outbox_events AS o
SET published_at = now(), seq = t.seq
FROM (
    -- Two unnests in one select list are zipped pairwise.
    SELECT unnest(sqlc.arg(ids)::bigint[]) AS id, unnest(sqlc.arg(seqs)::bigint[]) AS seq
) AS t
WHERE o.id = t.id AND o.published_at IS NULL;

-- name: PrunePublished :one
-- PrunePublished deletes one batch and raises the watermark to the highest
-- deleted seq in the same statement, so catch-up never misses a gap that
-- retention made (it answers resync_required instead).
WITH deleted AS (
    DELETE FROM outbox_events
    WHERE id IN (
        SELECT p.id FROM outbox_events AS p
        WHERE p.published_at < sqlc.arg(before)::timestamptz
        ORDER BY p.published_at
        LIMIT sqlc.arg(max_rows)
    )
    RETURNING seq
)
UPDATE outbox_watermark
SET pruned_seq = GREATEST(pruned_seq, COALESCE((SELECT max(d.seq) FROM deleted AS d), 0))
WHERE id
RETURNING (SELECT count(*) FROM deleted)::bigint AS deleted;

-- name: EventsAfter :many
-- EventsAfter is the catch-up read of the application role inside
-- InTenantTx: published events of one workspace after a seq, only topics
-- the caller may see (LIKE patterns such as 'conversation.%').
SELECT id, seq, topic, payload, created_at
FROM outbox_events
WHERE workspace_id = sqlc.arg(workspace_id)
  AND seq > sqlc.arg(after_seq)::bigint
  AND topic LIKE ANY (sqlc.arg(topic_patterns)::text[])
ORDER BY seq
LIMIT sqlc.arg(max_rows);

-- name: LatestSeq :one
-- LatestSeq is where a fresh client starts: the newest published seq of the
-- workspace, or the prune watermark if that is higher.
SELECT GREATEST(
    COALESCE((SELECT max(e.seq) FROM outbox_events AS e
              WHERE e.workspace_id = sqlc.arg(workspace_id) AND e.seq IS NOT NULL), 0),
    (SELECT w.pruned_seq FROM outbox_watermark AS w WHERE w.id)
)::bigint AS seq;

-- name: PrunedSeq :one
SELECT pruned_seq FROM outbox_watermark WHERE id;

-- name: IsSystemMember :one
SELECT pg_has_role(current_user, 'tolkovo_system', 'MEMBER');
