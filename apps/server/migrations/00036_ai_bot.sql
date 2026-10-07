-- The AI bot answers customers (ADR-0018, AI-04). Owned by internal/ai.
--
-- A new conversation of a channel the bot answers starts in status bot
-- instead of the queue; every customer message queues the bot's turn
-- (ai.reply). The bot answers from the knowledge base or hands the
-- conversation to people.

-- +goose Up

-- The channels whose new conversations the bot answers.
CREATE TABLE ai_bot_channels (
    workspace_id  uuid NOT NULL,
    channel_id    uuid NOT NULL,
    PRIMARY KEY (workspace_id, channel_id),
    FOREIGN KEY (workspace_id, channel_id) REFERENCES channels (workspace_id, id) ON DELETE CASCADE
);

-- Topics the bot leaves to people (a topic answers with the bot unless it
-- is here): their conversations are handed over at once.
CREATE TABLE ai_bot_topics_off (
    workspace_id  uuid NOT NULL,
    topic_id      uuid NOT NULL,
    PRIMARY KEY (workspace_id, topic_id),
    FOREIGN KEY (workspace_id, topic_id) REFERENCES topics (workspace_id, id) ON DELETE CASCADE
);

-- How sure the model must be of an answer from the knowledge base, in
-- percent, and what the customer reads when the bot hands over without
-- words of its own (empty: the built-in phrase in the workspace's language).
ALTER TABLE ai_bot_settings
    ADD COLUMN min_confidence  smallint NOT NULL DEFAULT 75 CHECK (min_confidence BETWEEN 0 AND 100),
    ADD COLUMN handoff_message text NOT NULL DEFAULT '' CHECK (char_length(handoff_message) <= 500);

-- The bot's state in a conversation. answered_up_to is the newest customer
-- message a turn took; a job claims the turn (claimed_by, its River job id)
-- while it asks the model, so two jobs never answer the same messages.
-- collected holds what the customer told for a task of the IT team.
CREATE TABLE ai_bot_conversations (
    workspace_id     uuid NOT NULL,
    conversation_id  uuid NOT NULL,
    answered_up_to   uuid,
    replies          integer NOT NULL DEFAULT 0 CHECK (replies >= 0),
    collect_turns    integer NOT NULL DEFAULT 0 CHECK (collect_turns >= 0),
    collected        jsonb NOT NULL DEFAULT '[]',
    claimed_by       bigint,
    claimed_at       timestamptz,
    updated_at       timestamptz NOT NULL,
    PRIMARY KEY (workspace_id, conversation_id),
    FOREIGN KEY (workspace_id, conversation_id) REFERENCES conversations (workspace_id, id) ON DELETE CASCADE
);

ALTER TABLE ai_bot_channels ENABLE ROW LEVEL SECURITY;
ALTER TABLE ai_bot_channels FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON ai_bot_channels
    USING (workspace_id = app_current_workspace())
    WITH CHECK (workspace_id = app_current_workspace());

ALTER TABLE ai_bot_topics_off ENABLE ROW LEVEL SECURITY;
ALTER TABLE ai_bot_topics_off FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON ai_bot_topics_off
    USING (workspace_id = app_current_workspace())
    WITH CHECK (workspace_id = app_current_workspace());

ALTER TABLE ai_bot_conversations ENABLE ROW LEVEL SECURITY;
ALTER TABLE ai_bot_conversations FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON ai_bot_conversations
    USING (workspace_id = app_current_workspace())
    WITH CHECK (workspace_id = app_current_workspace());

GRANT SELECT, INSERT, DELETE ON ai_bot_channels TO tolkovo_app;
GRANT SELECT, INSERT, DELETE ON ai_bot_topics_off TO tolkovo_app;
GRANT SELECT, INSERT, UPDATE ON ai_bot_conversations TO tolkovo_app;

-- +goose Down
DROP TABLE ai_bot_conversations;
ALTER TABLE ai_bot_settings DROP COLUMN handoff_message, DROP COLUMN min_confidence;
DROP TABLE ai_bot_topics_off;
DROP TABLE ai_bot_channels;
