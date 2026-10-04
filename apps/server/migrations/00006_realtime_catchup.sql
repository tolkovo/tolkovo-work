-- Realtime catch-up from the outbox (docs/adr/0005-realtime-transport.md,
-- backlog R-03). After a reconnect a client sends the last seq it saw; the
-- realtime gateway reads the missed events of its workspace from
-- outbox_events as the application role, so RLS still applies.
--
-- NO TRANSACTION: the index is built CONCURRENTLY, which cannot run inside a
-- transaction, so writers of outbox_events are never blocked (docs/00 §6.6).
-- Every statement is idempotent, so a half-applied run can be repeated.

-- +goose NO TRANSACTION

-- +goose Up
CREATE INDEX CONCURRENTLY IF NOT EXISTS outbox_events_workspace_seq
    ON outbox_events (workspace_id, seq) WHERE seq IS NOT NULL;

-- The highest seq that retention has deleted. A client whose last seq is
-- below it may have missed pruned events and must refetch over REST
-- (resync_required). Global and tenant-free: it holds one number.
CREATE TABLE IF NOT EXISTS outbox_watermark (
    id          boolean PRIMARY KEY DEFAULT true CHECK (id),
    pruned_seq  bigint NOT NULL DEFAULT 0 CHECK (pruned_seq >= 0)
);
INSERT INTO outbox_watermark (id) VALUES (true) ON CONFLICT (id) DO NOTHING;

-- The application reads events of its own workspace (tenant_isolation
-- policy) for catch-up; it still cannot change or delete them.
GRANT SELECT ON outbox_events TO tolkovo_app;
GRANT SELECT ON outbox_watermark TO tolkovo_app;
GRANT SELECT, UPDATE ON outbox_watermark TO tolkovo_system;

-- +goose Down
REVOKE SELECT ON outbox_events FROM tolkovo_app;
DROP TABLE IF EXISTS outbox_watermark;
DROP INDEX CONCURRENTLY IF EXISTS outbox_events_workspace_seq;
