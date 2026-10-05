-- The GitHub webhook (GH-03, ADR-0012, docs/02 §12.3). Owned by
-- internal/integrations.
--
-- GitHub posts the App's events to /hooks/github/{workspace_id}/{integration_id}.
-- A delivery may come twice (GitHub's own retry, a manual redelivery):
-- integration_deliveries remembers X-GitHub-Delivery for a week, and an id
-- seen before is acknowledged without doing anything. The rows are pruned
-- by the webhook itself, inside the workspace: no cross-tenant cleanup.

-- +goose Up

CREATE TABLE integration_deliveries (
    workspace_id    uuid NOT NULL,
    integration_id  uuid NOT NULL,
    delivery_id     text NOT NULL CHECK (char_length(delivery_id) BETWEEN 1 AND 100),
    received_at     timestamptz NOT NULL,
    PRIMARY KEY (workspace_id, integration_id, delivery_id),
    FOREIGN KEY (workspace_id, integration_id) REFERENCES integrations (workspace_id, id) ON DELETE CASCADE
);
-- Pruning of old deliveries.
CREATE INDEX integration_deliveries_received_idx ON integration_deliveries (workspace_id, integration_id, received_at);

ALTER TABLE integration_deliveries ENABLE ROW LEVEL SECURITY;
ALTER TABLE integration_deliveries FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON integration_deliveries
    USING (workspace_id = app_current_workspace())
    WITH CHECK (workspace_id = app_current_workspace());

GRANT SELECT, INSERT, DELETE ON integration_deliveries TO tolkovo_app;

-- Why a closed issue was closed: completed (fixed) brings a draft reply
-- about the fix, not_planned does not. NULL while open, and for issues
-- linked already closed.
ALTER TABLE issue_links ADD COLUMN close_reason text CHECK (close_reason IN ('completed', 'not_planned'));

-- +goose Down
ALTER TABLE issue_links DROP COLUMN close_reason;
DROP TABLE integration_deliveries;
