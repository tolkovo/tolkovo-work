-- Files of widget visitors (W-02), owned by internal/widget.
--
-- A visitor uploads a file before the message that carries it, and an
-- anonymous visitor has no conversation yet, so the file cannot be an
-- attachment row until the message is stored. Each upload is a row here:
-- the object key the server chose, the declared name, type and size, and
-- the session that may send it. Sending a message claims its uploads with
-- the message's Idempotency-Key (a retry with the same key claims them
-- again); conversations then attach the objects to the message. The worker
-- deletes rows an hour old, and the objects of those that never reached a
-- message.

-- +goose Up

CREATE TABLE widget_uploads (
    id            uuid PRIMARY KEY,
    workspace_id  uuid NOT NULL,
    channel_id    uuid NOT NULL,
    -- No foreign key: an ended session's unsent files must still be found
    -- and deleted.
    session_id    uuid NOT NULL,
    storage_key   text NOT NULL UNIQUE CHECK (storage_key LIKE 'ws/' || workspace_id::text || '/conv/%'),
    file_name     text NOT NULL CHECK (char_length(file_name) BETWEEN 1 AND 255),
    content_type  text NOT NULL CHECK (char_length(content_type) BETWEEN 3 AND 255),
    size          bigint NOT NULL CHECK (size > 0),
    -- The Idempotency-Key of the message that took the file; NULL until sent.
    message_key   text CHECK (char_length(message_key) <= 255),
    created_at    timestamptz NOT NULL,
    UNIQUE (workspace_id, id),
    FOREIGN KEY (workspace_id, channel_id) REFERENCES channels (workspace_id, id) ON DELETE CASCADE
);

CREATE INDEX widget_uploads_session_idx ON widget_uploads (workspace_id, session_id);
CREATE INDEX widget_uploads_created_idx ON widget_uploads (created_at);

ALTER TABLE widget_uploads ENABLE ROW LEVEL SECURITY;
ALTER TABLE widget_uploads FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON widget_uploads
    USING (workspace_id = app_current_workspace())
    WITH CHECK (workspace_id = app_current_workspace());
-- The worker finds old uploads in every workspace (ids only) and cleans
-- each one up in its tenant transaction.
CREATE POLICY system_see_old ON widget_uploads FOR SELECT TO tolkovo_system
    USING (created_at < now() - interval '1 hour');

GRANT SELECT, INSERT, UPDATE, DELETE ON widget_uploads TO tolkovo_app;
GRANT SELECT (id, workspace_id, created_at) ON widget_uploads TO tolkovo_system;

-- +goose Down
DROP TABLE widget_uploads;
