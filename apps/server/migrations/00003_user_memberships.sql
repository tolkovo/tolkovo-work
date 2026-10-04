-- A signed-in user lists and picks a workspace before one is chosen
-- (docs/adr/0003-multitenancy-rls.md, open question to A-02).
--
-- ADR-0003 planned a SECURITY DEFINER function, but the tables use FORCE ROW
-- LEVEL SECURITY, so a definer function owned by a non-superuser migration
-- role would still see no rows. Instead, db.InUserTx sets app.user_id and the
-- read-only policies below show that user's own memberships and the
-- workspaces they belong to. Permissive policies are OR-ed with
-- tenant_isolation; writes still require app.workspace_id.

-- +goose Up

-- User of the current transaction, set by db.InUserTx. NULL when unset, so
-- the policies match nothing in tenant and global transactions.
CREATE FUNCTION app_current_user_id() RETURNS uuid
    LANGUAGE sql STABLE
    AS $$ SELECT NULLIF(current_setting('app.user_id', true), '')::uuid $$;

CREATE POLICY own_memberships ON memberships FOR SELECT
    USING (user_id = app_current_user_id());

-- The subquery is itself filtered by the memberships policies.
CREATE POLICY member_workspaces ON workspaces FOR SELECT
    USING (id IN (SELECT m.workspace_id FROM memberships m WHERE m.user_id = app_current_user_id()));

-- Custom roles (v1.0) of the user's workspaces; system roles are visible anyway.
CREATE POLICY member_roles ON roles FOR SELECT
    USING (workspace_id IN (SELECT m.workspace_id FROM memberships m WHERE m.user_id = app_current_user_id()));

GRANT EXECUTE ON FUNCTION app_current_user_id() TO tolkovo_app;

-- +goose Down
DROP POLICY member_roles ON roles;
DROP POLICY member_workspaces ON workspaces;
DROP POLICY own_memberships ON memberships;
DROP FUNCTION app_current_user_id();
