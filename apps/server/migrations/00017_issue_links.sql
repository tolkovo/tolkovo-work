-- Issues from conversations (GH-02, ADR-0012, docs/02 §12.3). issue_links
-- is owned by internal/integrations; conversations.awaiting_fix by
-- internal/conversations.
--
-- An operator creates a GitHub issue from a conversation, or links the
-- conversation to an issue that exists: one bug, many complaints. A new
-- issue waits in 'creating' until the job github.create_issue has created
-- it on GitHub; the text is kept only until then (it lives in GitHub
-- afterwards) and for a retry when GitHub refused it ('failed').

-- +goose Up

CREATE TABLE issue_links (
    id               uuid PRIMARY KEY,
    workspace_id     uuid NOT NULL,
    -- The integration the issue was created or linked through; a
    -- disconnected one stays as 'disabled' for these rows.
    integration_id   uuid NOT NULL,
    conversation_id  uuid NOT NULL,
    provider         text NOT NULL CHECK (provider IN ('github')),
    repo_id          bigint NOT NULL,
    repo_full_name   text NOT NULL CHECK (char_length(repo_full_name) BETWEEN 1 AND 200),
    -- creating: the job has not created it yet; open, closed: as on
    -- GitHub; failed: GitHub refused it, error_code says why.
    status           text NOT NULL CHECK (status IN ('creating', 'open', 'closed', 'failed')),
    error_code       text CHECK (char_length(error_code) <= 50),
    -- Set once the issue exists on GitHub.
    number           bigint CHECK (number > 0),
    node_id          text CHECK (char_length(node_id) <= 100),
    url              text CHECK (char_length(url) <= 500),
    title            text NOT NULL CHECK (char_length(title) BETWEEN 1 AND 256),
    -- The text to create the issue with, the customer quote included;
    -- cleared once the issue exists.
    body             text CHECK (char_length(body) <= 30000),
    labels           text[] NOT NULL DEFAULT '{}' CHECK (cardinality(labels) <= 10),
    created_by       uuid,
    created_at       timestamptz NOT NULL,
    updated_at       timestamptz NOT NULL,
    closed_at        timestamptz,
    UNIQUE (workspace_id, id),
    CHECK ((status IN ('open', 'closed')) = (number IS NOT NULL)),
    CHECK (status <> 'failed' OR error_code IS NOT NULL),
    FOREIGN KEY (workspace_id, integration_id) REFERENCES integrations (workspace_id, id) ON DELETE CASCADE,
    FOREIGN KEY (workspace_id, conversation_id) REFERENCES conversations (workspace_id, id) ON DELETE CASCADE
);

-- A conversation is linked to an issue once.
CREATE UNIQUE INDEX issue_links_issue_uq ON issue_links (workspace_id, conversation_id, repo_id, number)
    WHERE number IS NOT NULL;
-- One issue is being created per conversation at a time: a double click
-- does not make two.
CREATE UNIQUE INDEX issue_links_creating_uq ON issue_links (workspace_id, conversation_id)
    WHERE status = 'creating';
-- The webhook finds every conversation of an issue (GH-03).
CREATE INDEX issue_links_number_idx ON issue_links (workspace_id, integration_id, repo_id, number)
    WHERE number IS NOT NULL;

ALTER TABLE issue_links ENABLE ROW LEVEL SECURITY;
ALTER TABLE issue_links FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON issue_links
    USING (workspace_id = app_current_workspace())
    WITH CHECK (workspace_id = app_current_workspace());

GRANT SELECT, INSERT, UPDATE, DELETE ON issue_links TO tolkovo_app;

-- The conversation waits for a fix while an issue linked to it is open
-- (docs/02 §8): it does not resolve itself by inactivity.
ALTER TABLE conversations ADD COLUMN awaiting_fix boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE conversations DROP COLUMN awaiting_fix;
DROP TABLE issue_links;
