-- name: BotAnswersChannel :one
-- The bot takes new conversations of the channel: it is on there and AI is
-- on in the workspace.
SELECT EXISTS (
    SELECT 1 FROM ai_bot_channels c
    JOIN ai_settings s ON s.workspace_id = c.workspace_id
    WHERE c.workspace_id = @workspace_id AND c.channel_id = @channel_id AND s.enabled
) AS answers;

-- name: BotTopicOff :one
SELECT EXISTS (
    SELECT 1 FROM ai_bot_topics_off WHERE workspace_id = @workspace_id AND topic_id = @topic_id
) AS off;

-- name: GetBotSettings :one
-- The settings of the bot; a workspace without the row has the defaults.
SELECT coalesce((SELECT min_confidence FROM ai_bot_settings b WHERE b.workspace_id = @workspace_id), 75)::smallint AS min_confidence,
       coalesce((SELECT handoff_message FROM ai_bot_settings b WHERE b.workspace_id = @workspace_id), '')::text AS handoff_message,
       coalesce((SELECT array_agg(c.channel_id ORDER BY c.channel_id) FROM ai_bot_channels c
                 WHERE c.workspace_id = @workspace_id), '{}')::uuid[] AS channel_ids,
       coalesce((SELECT array_agg(t.topic_id ORDER BY t.topic_id) FROM ai_bot_topics_off t
                 WHERE t.workspace_id = @workspace_id), '{}')::uuid[] AS topics_off;

-- name: SetBotOptions :exec
INSERT INTO ai_bot_settings (workspace_id, min_confidence, handoff_message, updated_at)
VALUES (@workspace_id, @min_confidence, @handoff_message, @now)
ON CONFLICT (workspace_id) DO UPDATE
SET min_confidence = excluded.min_confidence, handoff_message = excluded.handoff_message, updated_at = excluded.updated_at;

-- name: ClearBotChannels :exec
DELETE FROM ai_bot_channels WHERE workspace_id = @workspace_id;

-- name: AddBotChannels :execrows
-- Channels of the workspace only: a foreign or missing id adds nothing.
INSERT INTO ai_bot_channels (workspace_id, channel_id)
SELECT @workspace_id, c.id FROM channels c
WHERE c.workspace_id = @workspace_id AND c.id = ANY(@channel_ids::uuid[])
ON CONFLICT DO NOTHING;

-- name: ClearBotTopicsOff :exec
DELETE FROM ai_bot_topics_off WHERE workspace_id = @workspace_id;

-- name: AddBotTopicsOff :execrows
INSERT INTO ai_bot_topics_off (workspace_id, topic_id)
SELECT @workspace_id, t.id FROM topics t
WHERE t.workspace_id = @workspace_id AND t.id = ANY(@topic_ids::uuid[])
ON CONFLICT DO NOTHING;

-- name: EnsureBotConversation :exec
INSERT INTO ai_bot_conversations (workspace_id, conversation_id, updated_at)
VALUES (@workspace_id, @conversation_id, @now)
ON CONFLICT DO NOTHING;

-- name: LockBotConversation :one
SELECT * FROM ai_bot_conversations
WHERE workspace_id = @workspace_id AND conversation_id = @conversation_id
FOR UPDATE;

-- name: ClaimBotTurn :exec
UPDATE ai_bot_conversations
SET claimed_by = @job_id, claimed_at = @now::timestamptz, updated_at = @now::timestamptz
WHERE workspace_id = @workspace_id AND conversation_id = @conversation_id;

-- name: ReleaseBotTurn :exec
-- Gives a claim back without a turn (waiting for the topic, a failure).
UPDATE ai_bot_conversations
SET claimed_by = NULL, claimed_at = NULL, updated_at = @now
WHERE workspace_id = @workspace_id AND conversation_id = @conversation_id AND claimed_by = @job_id;

-- name: FinishBotTurn :exec
UPDATE ai_bot_conversations
SET answered_up_to = @answered_up_to, replies = @replies, collect_turns = @collect_turns, collected = @collected,
    claimed_by = NULL, claimed_at = NULL, updated_at = @now
WHERE workspace_id = @workspace_id AND conversation_id = @conversation_id;
