-- Background jobs (River) and the transactional outbox.
-- Decisions: docs/adr/0004-river-background-jobs.md, docs/adr/0003-multitenancy-rls.md.

-- +goose Up

-- System processes (outbox relay, retention) connect as a LOGIN role that is
-- a member of tolkovo_system. It has no BYPASSRLS: every cross-tenant table it
-- may touch gets an explicit policy below, so its reach is visible in SQL.
-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'tolkovo_system') THEN
        CREATE ROLE tolkovo_system NOLOGIN;
    END IF;
EXCEPTION
    WHEN duplicate_object OR unique_violation THEN NULL;
END
$$;
-- +goose StatementEnd

-- River's tables live in their own schema and are created by rivermigrate
-- right after goose (internal/platform/migrate). The queue is not tenant
-- data: job arguments carry workspace_id and workers open InTenantTx.
CREATE SCHEMA river;
GRANT USAGE ON SCHEMA river TO tolkovo_app;
-- Applies to objects the migration owner creates later in this schema.
ALTER DEFAULT PRIVILEGES IN SCHEMA river
    GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO tolkovo_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA river
    GRANT USAGE, SELECT, UPDATE ON SEQUENCES TO tolkovo_app;

-- Domain events written in the same transaction as the change they describe.
-- id orders inserts; seq orders publication. Ids can commit out of order
-- (11 before 10), so consumers that resume after a gap use seq, which the
-- single relay assigns at publish time.
CREATE TABLE outbox_events (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    workspace_id  uuid NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    topic         text NOT NULL
                  CHECK (topic ~ '^[a-z][a-z_]*(\.[a-z][a-z_]*)+$' AND char_length(topic) <= 100),
    payload       jsonb NOT NULL DEFAULT '{}',
    created_at    timestamptz NOT NULL DEFAULT now(),
    published_at  timestamptz,
    seq           bigint UNIQUE,
    CHECK ((published_at IS NULL) = (seq IS NULL))
);
CREATE INDEX outbox_events_unpublished ON outbox_events (id) WHERE published_at IS NULL;
CREATE INDEX outbox_events_published_at ON outbox_events (published_at) WHERE published_at IS NOT NULL;
CREATE INDEX outbox_events_workspace ON outbox_events (workspace_id);

CREATE SEQUENCE outbox_events_seq AS bigint OWNED BY outbox_events.seq;

ALTER TABLE outbox_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE outbox_events FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON outbox_events
    USING (workspace_id = app_current_workspace())
    WITH CHECK (workspace_id = app_current_workspace());
-- The relay reads, marks and prunes events of every workspace.
CREATE POLICY system_relay ON outbox_events TO tolkovo_system
    USING (true);

-- The application only appends events.
GRANT INSERT ON outbox_events TO tolkovo_app;
GRANT SELECT, DELETE ON outbox_events TO tolkovo_system;
GRANT UPDATE (published_at, seq) ON outbox_events TO tolkovo_system;
-- nextval needs USAGE, setval needs UPDATE (the relay reserves seq ranges).
GRANT USAGE, UPDATE ON SEQUENCE outbox_events_seq TO tolkovo_system;

-- +goose Down
DROP TABLE outbox_events;
-- River's tables go with the schema, so River needs no down step of its own.
DROP SCHEMA river CASCADE;
-- Roles are cluster-wide and may serve other databases, so they stay.
