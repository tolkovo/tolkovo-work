-- Invitations of people into a workspace by email (docs/06 A-03).
--
-- The link in the email carries a random token; only its SHA-256 is stored.
-- The token is issued by the job that sends the email (and again on every
-- resend), so it never sits in the job table, and only the latest link of an
-- invitation works. The token also names the workspace, so accepting looks
-- the invitation up inside that workspace's tenant transaction and no
-- cross-tenant policy is needed.

-- +goose Up
CREATE TABLE invitations (
    id            uuid PRIMARY KEY,
    workspace_id  uuid NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    email         citext NOT NULL CHECK (char_length(email) BETWEEN 3 AND 254),
    role_id       uuid NOT NULL REFERENCES roles (id),
    invited_by    uuid REFERENCES users (id) ON DELETE SET NULL,
    -- NULL until the first link is issued (email sent or link copied).
    token_hash    bytea UNIQUE CHECK (octet_length(token_hash) = 32),
    expires_at    timestamptz NOT NULL,
    accepted_at   timestamptz,
    accepted_by   uuid REFERENCES users (id) ON DELETE SET NULL,
    revoked_at    timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now(),
    CHECK (accepted_at IS NULL OR revoked_at IS NULL)
);
-- One open invitation per address: inviting again replaces it.
CREATE UNIQUE INDEX invitations_open_email_uq ON invitations (workspace_id, email)
    WHERE accepted_at IS NULL AND revoked_at IS NULL;
CREATE INDEX invitations_workspace_idx ON invitations (workspace_id, created_at);

ALTER TABLE invitations ENABLE ROW LEVEL SECURITY;
ALTER TABLE invitations FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON invitations
    USING (workspace_id = app_current_workspace())
    WITH CHECK (workspace_id = app_current_workspace());

GRANT SELECT, INSERT, UPDATE ON invitations TO tolkovo_app;

-- +goose Down
DROP TABLE invitations;
