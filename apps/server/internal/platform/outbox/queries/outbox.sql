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

-- name: PrunePublished :execrows
DELETE FROM outbox_events
WHERE id IN (
    SELECT p.id FROM outbox_events AS p
    WHERE p.published_at < sqlc.arg(before)::timestamptz
    ORDER BY p.published_at
    LIMIT sqlc.arg(max_rows)
);

-- name: IsSystemMember :one
SELECT pg_has_role(current_user, 'tolkovo_system', 'MEMBER');
