-- Notifications of operators (I-05, docs/04 §5.5). Owned by
-- internal/notifications.
--
-- Sounds and desktop notifications are settings of a browser and stay in
-- it; only what the server acts on is stored: the email about messages a
-- member missed. Defaults apply while a member has no row.

-- +goose Up

CREATE TABLE notification_prefs (
    workspace_id         uuid NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    user_id              uuid NOT NULL,
    email_missed         boolean NOT NULL DEFAULT true,
    -- How long a message may stay unread before the email goes out.
    email_delay_minutes  smallint NOT NULL DEFAULT 15 CHECK (email_delay_minutes IN (5, 15, 30, 60)),
    updated_at           timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, user_id),
    FOREIGN KEY (workspace_id, user_id) REFERENCES memberships (workspace_id, user_id) ON DELETE CASCADE
);

-- What the last email about a conversation covered, so the same unread
-- messages are not emailed twice.
CREATE TABLE missed_email_marks (
    workspace_id     uuid NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    conversation_id  uuid NOT NULL,
    user_id          uuid NOT NULL,
    -- last_message_at of the conversation when the email went out.
    covered_up_to    timestamptz NOT NULL,
    sent_at          timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, conversation_id, user_id),
    FOREIGN KEY (workspace_id, conversation_id) REFERENCES conversations (workspace_id, id),
    FOREIGN KEY (workspace_id, user_id) REFERENCES memberships (workspace_id, user_id) ON DELETE CASCADE
);

ALTER TABLE notification_prefs ENABLE ROW LEVEL SECURITY;
ALTER TABLE notification_prefs FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON notification_prefs
    USING (workspace_id = app_current_workspace())
    WITH CHECK (workspace_id = app_current_workspace());

ALTER TABLE missed_email_marks ENABLE ROW LEVEL SECURITY;
ALTER TABLE missed_email_marks FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON missed_email_marks
    USING (workspace_id = app_current_workspace())
    WITH CHECK (workspace_id = app_current_workspace());

GRANT SELECT, INSERT, UPDATE ON notification_prefs TO tolkovo_app;
GRANT SELECT, INSERT, UPDATE ON missed_email_marks TO tolkovo_app;

-- +goose Down
DROP TABLE missed_email_marks;
DROP TABLE notification_prefs;
