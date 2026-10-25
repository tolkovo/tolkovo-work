package channels

import (
	"context"
	"strconv"

	"github.com/google/uuid"

	"github.com/tolkovo/tolkovo/apps/server/internal/channels/telegram"
	"github.com/tolkovo/tolkovo/apps/server/internal/conversations"
)

// "Call a person" in the Telegram bot (ADR-0018, AI-05): a reply of the AI
// bot, while it answers the conversation, carries a button; a press is
// stored as the customer's message with the button's words, and the bot's
// next turn hands the conversation to people as if they had typed it. A
// personal account cannot send buttons: there the customer writes it.

// personData is the data of the button.
const personData = "person"

// PersonLabel is the button's words, and the customer's message a press
// stores, in the workspace's language; the bot hears a request for a
// person in both.
func PersonLabel(locale string) string {
	if locale == "en" {
		return "Talk to an operator"
	}
	return "Позвать оператора"
}

func personButton(locale string) [][]telegram.Button {
	return [][]telegram.Button{{{Text: PersonLabel(locale), Data: personData}}}
}

// callback handles a press of one of the bot's buttons.
func (s *Service) callback(ctx context.Context, workspaceID, channelID uuid.UUID, token string, cb telegram.Callback) {
	if cb.Data == personData {
		s.callPerson(ctx, workspaceID, channelID, token, cb)
		return
	}
	s.chooseTopic(ctx, workspaceID, channelID, token, cb)
}

// callPerson stores the press as the customer's message and drops the
// button, best effort like every press: the customer can still write.
func (s *Service) callPerson(ctx context.Context, workspaceID, channelID uuid.UUID, token string, cb telegram.Callback) {
	defer func() {
		if err := s.opts.Bots.AnswerCallback(ctx, token, cb.ID, ""); err != nil {
			s.log.Warn("telegram callback not answered", "workspace_id", workspaceID, "channel_id", channelID, "error", err)
		}
	}()
	locale := "ru"
	if q, err := s.opts.Inbox.TopicQuestion(ctx, workspaceID); err == nil {
		locale = q.Locale
	}
	res, err := s.opts.Inbox.Receive(ctx, workspaceID, conversations.Inbound{
		// A press has no message id: its callback id keeps a resent press
		// from being stored twice.
		ChannelID: channelID, Thread: threadOf(cb.ChatID), ExternalID: "cb:" + cb.ID,
		Sender: conversations.InboundSender{
			ExternalUserID: strconv.FormatInt(cb.From.ID, 10), Name: cb.From.Name(), Username: cb.From.Username,
		},
		Body: PersonLabel(locale),
	})
	if err != nil {
		s.log.Warn("telegram call for a person not stored", "workspace_id", workspaceID, "channel_id", channelID, "error", err)
		return
	}
	if res.Stored {
		s.markInbound(ctx, workspaceID, channelID)
	}
	if err := s.opts.Bots.DropButtons(ctx, token, cb.ChatID, cb.MessageID); err != nil {
		s.log.Warn("telegram person button not dropped", "workspace_id", workspaceID, "channel_id", channelID, "error", err)
	}
}
