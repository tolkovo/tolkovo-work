-- Tenancy core: workspaces, global users, system roles, memberships.
-- Tenant isolation model: docs/adr/0003-multitenancy-rls.md.

-- +goose Up
CREATE EXTENSION IF NOT EXISTS citext;

-- The application connects as a LOGIN role that is a member of tolkovo_app.
-- tolkovo_app owns nothing and has no BYPASSRLS, so RLS applies to it.
-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'tolkovo_app') THEN
        CREATE ROLE tolkovo_app NOLOGIN;
    END IF;
EXCEPTION
    -- Roles are cluster-wide: a parallel migration of another database may
    -- create it between the check and CREATE ROLE.
    WHEN duplicate_object OR unique_violation THEN NULL;
END
$$;
-- +goose StatementEnd

-- Workspace of the current transaction, set by db.InTenantTx via
-- set_config('app.workspace_id', ..., true). NULL when unset, so policies
-- match no rows instead of failing. NULLIF covers the empty string a pooled
-- connection keeps after a previous transaction.
CREATE FUNCTION app_current_workspace() RETURNS uuid
    LANGUAGE sql STABLE
    AS $$ SELECT NULLIF(current_setting('app.workspace_id', true), '')::uuid $$;

CREATE TABLE workspaces (
    id          uuid PRIMARY KEY,
    slug        citext NOT NULL UNIQUE
                CHECK (slug ~ '^[a-z0-9][a-z0-9-]{1,38}[a-z0-9]$'),
    name        text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 120),
    locale      text NOT NULL DEFAULT 'ru' CHECK (locale IN ('ru', 'en')),
    timezone    text NOT NULL DEFAULT 'Europe/Moscow',
    limits      jsonb NOT NULL DEFAULT '{}',
    settings    jsonb NOT NULL DEFAULT '{}',
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    deleted_at  timestamptz
);

-- Users are global: one person can belong to several workspaces.
CREATE TABLE users (
    id                 uuid PRIMARY KEY,
    email              citext NOT NULL UNIQUE CHECK (char_length(email) BETWEEN 3 AND 254),
    password_hash      text,
    name               text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 120),
    locale             text NOT NULL DEFAULT 'ru' CHECK (locale IN ('ru', 'en')),
    email_verified_at  timestamptz,
    last_login_at      timestamptz,
    disabled_at        timestamptz,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now()
);

-- workspace_id IS NULL marks a system role shared by every workspace.
CREATE TABLE roles (
    id            uuid PRIMARY KEY,
    workspace_id  uuid REFERENCES workspaces (id) ON DELETE CASCADE,
    key           text NOT NULL CHECK (key ~ '^[a-z][a-z0-9_]{1,39}$'),
    name          text NOT NULL,
    is_system     boolean NOT NULL DEFAULT false,
    created_at    timestamptz NOT NULL DEFAULT now(),
    CHECK (is_system = (workspace_id IS NULL))
);
CREATE UNIQUE INDEX roles_system_key_uq ON roles (key) WHERE workspace_id IS NULL;
CREATE UNIQUE INDEX roles_workspace_key_uq ON roles (workspace_id, key) WHERE workspace_id IS NOT NULL;

-- Phase 1 roles (docs/README.md §4.4). Supervisor and the rest come with
-- their own migrations. Seeded before RLS is enabled: the policy forbids
-- writing rows with workspace_id IS NULL.
INSERT INTO roles (id, workspace_id, key, name, is_system) VALUES
    ('00000000-0000-7000-8000-000000000001', NULL, 'owner',    'Owner',    true),
    ('00000000-0000-7000-8000-000000000002', NULL, 'admin',    'Admin',    true),
    ('00000000-0000-7000-8000-000000000003', NULL, 'operator', 'Operator', true);

CREATE TABLE memberships (
    workspace_id          uuid NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    user_id               uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role_id               uuid NOT NULL REFERENCES roles (id),
    display_name          text CHECK (char_length(display_name) BETWEEN 1 AND 120),
    max_concurrent_chats  integer NOT NULL DEFAULT 3 CHECK (max_concurrent_chats BETWEEN 1 AND 50),
    line_level            smallint NOT NULL DEFAULT 1 CHECK (line_level BETWEEN 1 AND 3),
    skills                text[] NOT NULL DEFAULT '{}',
    is_active             boolean NOT NULL DEFAULT true,
    created_at            timestamptz NOT NULL DEFAULT now(),
    updated_at            timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, user_id)
);
CREATE INDEX memberships_user_idx ON memberships (user_id);

ALTER TABLE workspaces ENABLE ROW LEVEL SECURITY;
ALTER TABLE workspaces FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON workspaces
    USING (id = app_current_workspace())
    WITH CHECK (id = app_current_workspace());

ALTER TABLE roles ENABLE ROW LEVEL SECURITY;
ALTER TABLE roles FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON roles
    USING (workspace_id IS NULL OR workspace_id = app_current_workspace())
    WITH CHECK (workspace_id = app_current_workspace());

ALTER TABLE memberships ENABLE ROW LEVEL SECURITY;
ALTER TABLE memberships FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON memberships
    USING (workspace_id = app_current_workspace())
    WITH CHECK (workspace_id = app_current_workspace());

GRANT EXECUTE ON FUNCTION app_current_workspace() TO tolkovo_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON workspaces, users, roles, memberships TO tolkovo_app;

-- +goose Down
-- The tolkovo_app role is cluster-wide and may serve other databases, so it
-- is not dropped here.
DROP TABLE memberships;
DROP TABLE roles;
DROP TABLE users;
DROP TABLE workspaces;
DROP FUNCTION app_current_workspace();
