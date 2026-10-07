package channels

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"

	"github.com/tolkovo/tolkovo/apps/server/internal/channels/telegram"
	"github.com/tolkovo/tolkovo/apps/server/internal/conversations"
)

// Topics in the Telegram bot (docs/02 §7.3): after the first message of a
// conversation the bot asks what the question is about with a button per
// topic; a press records the topic and leaves the chosen one in place of
// the buttons. A personal account cannot send buttons: an operator sets
// the topic there.

// topicData prefixes the data of a topic button: "topic:<uuid>" fits the
// 64 bytes Telegram allows.
const topicData = "topic:"

// topicWords are the bot's words around the topics, in the workspace's
// language.
type topicWords struct {
	question, chosen, unavailable string
}

func wordsFor(locale string) topicWords {
	if locale == "en" {
		return topicWords{
			question:    "Choose the topic of your question so we can answer faster:",
			chosen:      "Topic: ",
			unavailable: "This topic is no longer available. Write to us, please.",
		}
	}
	return topicWords{
		question:    "Выберите тему вопроса — так мы ответим быстрее:",
		chosen:      "Тема вопроса: ",
		unavailable: "Эта тема больше недоступна. Напишите нам, пожалуйста.",
	}
}

// askTopic sends the topic buttons to a chat, best effort: the message is
// stored, and an operator can set the topic too.
func (s *Service) askTopic(ctx context.Context, workspaceID, channelID uuid.UUID, token string, chatID int64) {
	q, err := s.opts.Inbox.TopicQuestion(ctx, workspaceID)
	if err != nil || len(q.Topics) == 0 {
		if err != nil {
			s.log.Warn("telegram topic question", "workspace_id", workspaceID, "channel_id", channelID, "error", err)
		}
		return
	}
	rows := make([][]telegram.Button, 0, len(q.Topics))
	for _, t := range q.Topics {
		rows = append(rows, []telegram.Button{{Text: t.Name, Data: topicData + t.ID.String()}})
	}
	if _, err := s.opts.Bots.SendButtons(ctx, token, chatID, wordsFor(q.Locale).question, rows); err != nil {
		s.log.Warn("telegram topic buttons not sent", "workspace_id", workspaceID, "channel_id", channelID, "error", err)
	}
}

// chooseTopic handles a press of a topic button: the topic goes to the
// chat's open conversation and replaces the buttons. Other buttons, a
// deleted topic or a chat without an open conversation get a short answer.
// Best effort: Telegram does not resend a press, and the customer can
// press again.
func (s *Service) chooseTopic(ctx context.Context, workspaceID, channelID uuid.UUID, token string, cb telegram.Callback) {
	answer := ""
	defer func() {
		if err := s.opts.Bots.AnswerCallback(ctx, token, cb.ID, answer); err != nil {
			s.log.Warn("telegram callback not answered", "workspace_id", workspaceID, "channel_id", channelID, "error", err)
		}
	}()
	raw, ok := strings.CutPrefix(cb.Data, topicData)
	if !ok {
		return
	}
	q, err := s.opts.Inbox.TopicQuestion(ctx, workspaceID)
	if err != nil {
		s.log.Warn("telegram topic question", "workspace_id", workspaceID, "channel_id", channelID, "error", err)
		return
	}
	words := wordsFor(q.Locale)
	id, err := uuid.Parse(raw)
	if err != nil {
		answer = words.unavailable
		return
	}
	t, err := s.opts.Inbox.ChooseThreadTopic(ctx, workspaceID, channelID, threadOf(cb.ChatID), id)
	if errors.Is(err, conversations.ErrNotFound) {
		answer = words.unavailable
		return
	}
	if err != nil {
		s.log.Warn("telegram topic not chosen", "workspace_id", workspaceID, "channel_id", channelID, "error", err)
		return
	}
	if err := s.opts.Bots.EditText(ctx, token, cb.ChatID, cb.MessageID, words.chosen+t.Name); err != nil {
		s.log.Warn("telegram topic buttons not replaced", "workspace_id", workspaceID, "channel_id", channelID, "error", err)
	}
}
