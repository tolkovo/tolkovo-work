-- Tags and read marks of the inbox (I-02, docs/02 §7.2, §10.5). Owned by
-- internal/conversations.
--
-- Read marks are per operator, not one column on conversations: a
-- supervisor glancing at a conversation must not clear the assignee's
-- unread counter. The customer's read mark (widget, W-01) comes later.

-- +goose Up

CREATE TABLE tags (
    id            uuid PRIMARY KEY,
    workspace_id  uuid NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    name          citext NOT NULL CHECK (char_length(name) BETWEEN 1 AND 40),
    -- A palette key of the panel, not a free color: tags stay readable in
    -- both themes.
    color         text NOT NULL DEFAULT 'gray'
                  CHECK (color IN ('gray', 'teal', 'amber', 'red', 'violet', 'blue', 'green')),
    created_by    uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, name)
);

CREATE TABLE conversation_tags (
    workspace_id     uuid NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    conversation_id  uuid NOT NULL,
    tag_id           uuid NOT NULL,
    created_by       uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, conversation_id, tag_id),
    FOREIGN KEY (workspace_id, conversation_id) REFERENCES conversations (workspace_id, id),
    FOREIGN KEY (workspace_id, tag_id) REFERENCES tags (workspace_id, id)
);
-- The tag filter of the inbox (docs/04 §3.1) and usage counts.
CREATE INDEX conversation_tags_tag_idx ON conversation_tags (workspace_id, tag_id);

CREATE TABLE conversation_reads (
    workspace_id          uuid NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    conversation_id       uuid NOT NULL,
    user_id               uuid NOT NULL,
    -- Messages up to this id (UUIDv7, so in creation order) are read.
    last_read_message_id  uuid NOT NULL,
    read_at               timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, conversation_id, user_id),
    FOREIGN KEY (workspace_id, conversation_id) REFERENCES conversations (workspace_id, id),
    FOREIGN KEY (workspace_id, last_read_message_id) REFERENCES messages (workspace_id, id),
    FOREIGN KEY (workspace_id, user_id) REFERENCES memberships (workspace_id, user_id) ON DELETE CASCADE
);

ALTER TABLE tags ENABLE ROW LEVEL SECURITY;
ALTER TABLE tags FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON tags
    USING (workspace_id = app_current_workspace())
    WITH CHECK (workspace_id = app_current_workspace());

ALTER TABLE conversation_tags ENABLE ROW LEVEL SECURITY;
ALTER TABLE conversation_tags FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON conversation_tags
    USING (workspace_id = app_current_workspace())
    WITH CHECK (workspace_id = app_current_workspace());

ALTER TABLE conversation_reads ENABLE ROW LEVEL SECURITY;
ALTER TABLE conversation_reads FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON conversation_reads
    USING (workspace_id = app_current_workspace())
    WITH CHECK (workspace_id = app_current_workspace());

-- Renaming and deleting tags come with the tags settings page.
GRANT SELECT, INSERT ON tags TO tolkovo_app;
GRANT SELECT, INSERT, DELETE ON conversation_tags TO tolkovo_app;
GRANT SELECT, INSERT, UPDATE ON conversation_reads TO tolkovo_app;

-- +goose Down
DROP TABLE conversation_reads;
DROP TABLE conversation_tags;
DROP TABLE tags;
