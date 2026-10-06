-- Distribution of the queue among operators (docs/02 §9). Owned by
-- internal/routing.
--
-- Phase 1 has no lines, teams, skills or shifts: the router gives queued
-- conversations in turn to the members who accept them and have the panel
-- open. A member chooses "accepting" (online) or "away"; whether the panel
-- is open is the heartbeat the realtime gateway writes into seen_at.

-- +goose Up

CREATE TABLE routing_settings (
    workspace_id  uuid PRIMARY KEY REFERENCES workspaces (id) ON DELETE CASCADE,
    -- manual: operators take conversations from the queue themselves;
    -- round_robin: the router assigns them in turn.
    mode          text NOT NULL DEFAULT 'manual' CHECK (mode IN ('manual', 'round_robin')),
    updated_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE operator_states (
    workspace_id      uuid NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    user_id           uuid NOT NULL,
    status            text NOT NULL DEFAULT 'online' CHECK (status IN ('online', 'away')),
    -- Last heartbeat of an open panel; the member is offline when it is old.
    seen_at           timestamptz,
    -- When the router last gave the member a conversation: the turn order.
    last_assigned_at  timestamptz,
    updated_at        timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, user_id),
    FOREIGN KEY (workspace_id, user_id) REFERENCES memberships (workspace_id, user_id) ON DELETE CASCADE
);

-- The queue in the order the router takes it (conversations belongs to
-- internal/conversations, which runs that query for the router).
CREATE INDEX conversations_queue_idx ON conversations (workspace_id, priority DESC, created_at, id)
    WHERE status = 'queued';

ALTER TABLE routing_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE routing_settings FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON routing_settings
    USING (workspace_id = app_current_workspace())
    WITH CHECK (workspace_id = app_current_workspace());

ALTER TABLE operator_states ENABLE ROW LEVEL SECURITY;
ALTER TABLE operator_states FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON operator_states
    USING (workspace_id = app_current_workspace())
    WITH CHECK (workspace_id = app_current_workspace());

GRANT SELECT, INSERT, UPDATE ON routing_settings TO tolkovo_app;
GRANT SELECT, INSERT, UPDATE ON operator_states TO tolkovo_app;

-- +goose Down
DROP INDEX conversations_queue_idx;
DROP TABLE operator_states;
DROP TABLE routing_settings;
