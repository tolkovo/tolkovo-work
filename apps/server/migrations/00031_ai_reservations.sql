-- Reservations of the monthly AI budget (docs/03 §9). Owned by internal/ai.
--
-- A call reserves the most it may cost before it leaves for the provider,
-- under a lock of the workspace, and gives the reservation back when its
-- row goes to ai_calls. The budget check counts what is spent and what is
-- reserved, so parallel calls cannot all pass a budget only one of them
-- fits. A reservation left by a stopped server expires on its own.

-- +goose Up

CREATE TABLE ai_reservations (
    id               uuid PRIMARY KEY,
    workspace_id     uuid NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    reserved_micros  bigint NOT NULL CHECK (reserved_micros >= 0),
    expires_at       timestamptz NOT NULL
);
CREATE INDEX ai_reservations_workspace_idx ON ai_reservations (workspace_id, expires_at);

ALTER TABLE ai_reservations ENABLE ROW LEVEL SECURITY;
ALTER TABLE ai_reservations FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON ai_reservations
    USING (workspace_id = app_current_workspace())
    WITH CHECK (workspace_id = app_current_workspace());

GRANT SELECT, INSERT, DELETE ON ai_reservations TO tolkovo_app;

-- +goose Down
DROP TABLE ai_reservations;
