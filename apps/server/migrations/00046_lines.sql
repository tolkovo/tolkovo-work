-- Lines of support L1/L2/L3 (ADR-0021, step 2). Owned by
-- internal/conversations, as topics are.
--
-- A line is a group of operators with a level 1–3. A conversation without
-- a line is on the first line, where new ones and those from the AI bot
-- land; escalation moves it to a line of a higher level, and back. The
-- router gives a conversation of a line to its members only; topics keep
-- distributing the first line.

-- +goose Up

CREATE TABLE lines (
    id            uuid NOT NULL,
    workspace_id  uuid NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    name          citext NOT NULL CHECK (char_length(name) BETWEEN 1 AND 60),
    level         smallint NOT NULL CHECK (level BETWEEN 1 AND 3),
    created_at    timestamptz NOT NULL,
    updated_at    timestamptz NOT NULL,
    PRIMARY KEY (workspace_id, id),
    UNIQUE (workspace_id, name)
);

CREATE TABLE line_members (
    workspace_id  uuid NOT NULL,
    line_id       uuid NOT NULL,
    user_id       uuid NOT NULL,
    PRIMARY KEY (workspace_id, line_id, user_id),
    FOREIGN KEY (workspace_id, line_id) REFERENCES lines (workspace_id, id) ON DELETE CASCADE,
    FOREIGN KEY (workspace_id, user_id) REFERENCES memberships (workspace_id, user_id) ON DELETE CASCADE
);

-- A deleted line sends its conversations back to the first line.
ALTER TABLE conversations
    ADD COLUMN line_id uuid,
    ADD FOREIGN KEY (workspace_id, line_id) REFERENCES lines (workspace_id, id) ON DELETE SET NULL (line_id);
-- The line filter of the inbox.
CREATE INDEX conversations_line_idx ON conversations (workspace_id, line_id, last_message_at DESC, id DESC)
    WHERE line_id IS NOT NULL;

ALTER TABLE lines ENABLE ROW LEVEL SECURITY;
ALTER TABLE lines FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON lines
    USING (workspace_id = app_current_workspace())
    WITH CHECK (workspace_id = app_current_workspace());

ALTER TABLE line_members ENABLE ROW LEVEL SECURITY;
ALTER TABLE line_members FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON line_members
    USING (workspace_id = app_current_workspace())
    WITH CHECK (workspace_id = app_current_workspace());

GRANT SELECT, INSERT, UPDATE, DELETE ON lines TO tolkovo_app;
GRANT SELECT, INSERT, DELETE ON line_members TO tolkovo_app;

-- +goose Down
DROP INDEX conversations_line_idx;
ALTER TABLE conversations DROP COLUMN line_id;
DROP TABLE line_members;
DROP TABLE lines;
