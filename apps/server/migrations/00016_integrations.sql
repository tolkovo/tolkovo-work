-- Integrations with outside trackers (GH-01, ADR-0012, docs/02 §12.3).
-- Owned by internal/integrations.
--
-- A workspace connects GitHub through its own GitHub App, created from a
-- manifest: the admin confirms the App on GitHub, GitHub hands the App's
-- private key and webhook secret back once, and the admin installs the App
-- on the repositories. The private key and the webhook secret live in
-- integration_secrets, sealed by internal/platform/crypto with
-- workspace_id and the integration id as associated data; integrations
-- holds only what the panel may show.

-- +goose Up

CREATE TABLE integrations (
    id                    uuid PRIMARY KEY,
    workspace_id          uuid NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    provider              text NOT NULL CHECK (provider IN ('github')),
    -- app_pending: the manifest went to GitHub, the App is not created
    -- yet; install_pending: the App exists, not installed; active: works;
    -- disabled: disconnected, kept for the issues linked to it (GH-02).
    status                text NOT NULL CHECK (status IN ('app_pending', 'install_pending', 'active', 'disabled')),
    -- The state parameter of the GitHub redirect: SHA-256 of a random
    -- token handed to the panel, so a code or an installation of somebody
    -- else's flow is refused.
    state_hash            bytea CHECK (octet_length(state_hash) = 32),
    state_expires_at      timestamptz,
    -- The GitHub App: its id, slug (github.com/apps/<slug>), owner login
    -- and settings page.
    app_id                bigint,
    app_slug              text CHECK (char_length(app_slug) <= 100),
    app_owner             text CHECK (char_length(app_owner) <= 100),
    app_url               text CHECK (char_length(app_url) <= 500),
    installation_id       bigint,
    -- The account the App is installed on.
    account               text CHECK (char_length(account) <= 100),
    -- The repository new issues go to; the form may pick another one.
    repo_id               bigint,
    repo_full_name        text CHECK (char_length(repo_full_name) <= 200),
    repo_private          boolean,
    labels                text[] NOT NULL DEFAULT '{}' CHECK (cardinality(labels) <= 10),
    -- Templates; NULL is the built-in text in the workspace language.
    issue_title_template  text CHECK (char_length(issue_title_template) BETWEEN 1 AND 256),
    issue_body_template   text CHECK (char_length(issue_body_template) BETWEEN 1 AND 20000),
    reply_template_ru     text CHECK (char_length(reply_template_ru) BETWEEN 1 AND 4000),
    reply_template_en     text CHECK (char_length(reply_template_en) BETWEEN 1 AND 4000),
    created_by            uuid,
    created_at            timestamptz NOT NULL DEFAULT now(),
    updated_at            timestamptz NOT NULL DEFAULT now(),
    disabled_at           timestamptz,
    UNIQUE (workspace_id, id),
    FOREIGN KEY (workspace_id, created_by) REFERENCES memberships (workspace_id, user_id) ON DELETE SET NULL (created_by)
);

-- One live integration per provider and workspace.
CREATE UNIQUE INDEX integrations_live_uq ON integrations (workspace_id, provider) WHERE status <> 'disabled';

CREATE TABLE integration_secrets (
    workspace_id    uuid NOT NULL,
    integration_id  uuid NOT NULL,
    sealed          bytea NOT NULL,
    updated_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, integration_id),
    FOREIGN KEY (workspace_id, integration_id) REFERENCES integrations (workspace_id, id) ON DELETE CASCADE
);

ALTER TABLE integrations ENABLE ROW LEVEL SECURITY;
ALTER TABLE integrations FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON integrations
    USING (workspace_id = app_current_workspace())
    WITH CHECK (workspace_id = app_current_workspace());

ALTER TABLE integration_secrets ENABLE ROW LEVEL SECURITY;
ALTER TABLE integration_secrets FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON integration_secrets
    USING (workspace_id = app_current_workspace())
    WITH CHECK (workspace_id = app_current_workspace());

-- Integrations are disabled, never deleted: issues keep pointing at them.
-- An unfinished flow is replaced by deleting its row.
GRANT SELECT, INSERT, UPDATE, DELETE ON integrations TO tolkovo_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON integration_secrets TO tolkovo_app;

-- +goose Down
DROP TABLE integration_secrets;
DROP TABLE integrations;
