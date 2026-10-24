package channels

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tolkovo/tolkovo/apps/server/internal/conversations"
)

// answeringBot takes every new conversation and counts its wake-ups.
type answeringBot struct{ woken atomic.Int32 }

func (*answeringBot) Answers(context.Context, pgx.Tx, uuid.UUID, uuid.UUID) (bool, error) {
	return true, nil
}

func (b *answeringBot) Wake(context.Context, pgx.Tx, uuid.UUID, uuid.UUID) error {
	b.woken.Add(1)
	return nil
}

// botReply records a turn of the bot and returns its reply.
func (f *fixture) botReply(conv uuid.UUID, t conversations.BotTurn) conversations.Message {
	f.t.Helper()
	err := f.app.InTenantTx(f.ctx, f.wsA, func(tx pgx.Tx) error {
		_, err := f.inbox.ApplyBotTurn(f.ctx, tx, f.wsA, conv, t)
		return err
	})
	if err != nil {
		f.t.Fatalf("bot turn: %v", err)
	}
	for _, m := range f.feedOf(f.wsA, conv) {
		if m.SenderType == conversations.SenderBot && m.Kind == conversations.KindText && m.Body == t.Reply {
			return m
		}
	}
	f.t.Fatalf("no bot reply %q", t.Reply)
	return conversations.Message{}
}

// A reply of the AI bot carries "Позвать оператора"; a press is the
// customer's request for a person, and replies after the handoff carry no
// button (AI-05).
func TestBotWebhook_CallPerson(t *testing.T) {
	f := sendSetup(t)
	bot := &answeringBot{}
	f.inbox.SetBot(bot)
	ch, conv := f.botConversation()
	if conv.Status != conversations.StatusBot {
		t.Fatalf("status %s", conv.Status)
	}

	m := f.botReply(conv.ID, conversations.BotTurn{Reply: "Здравствуйте! Я ИИ-ассистент."})
	if err := f.deliver(m.ID, false); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	sent := f.bots.SnapshotSent()
	last := sent[len(sent)-1]
	if last.Text != "Здравствуйте! Я ИИ-ассистент." || len(last.Buttons) != 1 ||
		last.Buttons[0][0].Text != "Позвать оператора" || last.Buttons[0][0].Data != personData {
		t.Fatalf("sent: %+v", last)
	}

	woken := bot.woken.Load()
	press := `{"update_id":110,"callback_query":{"id":"cb9","from":{"id":70001,"is_bot":false,"first_name":"Пётр"},` +
		`"message":{"message_id":1001,"chat":{"id":70001,"type":"private"},"date":2,"text":"…"},"data":"person"}}`
	for range 2 { // Telegram may resend an update
		if err := f.svc.BotWebhook(f.ctx, f.wsA, ch.ID, f.bots.Hooks[0].Secret, []byte(press)); err != nil {
			t.Fatalf("press: %v", err)
		}
	}
	var asked int
	for _, msg := range f.feedOf(f.wsA, conv.ID) {
		if msg.SenderType == conversations.SenderContact && msg.Body == "Позвать оператора" {
			asked++
		}
	}
	if asked != 1 || bot.woken.Load() != woken+1 {
		t.Fatalf("stored %d presses, woke the bot %d times", asked, bot.woken.Load()-woken)
	}
	answers, edits := f.bots.SnapshotAnswers()
	if len(answers) != 2 || answers[0].CallbackID != "cb9" || len(edits) == 0 || !edits[0].Dropped || edits[0].MessageID != 1001 {
		t.Fatalf("answers %+v, edits %+v", answers, edits)
	}

	m = f.botReply(conv.ID, conversations.BotTurn{Reply: "Передаю ваш вопрос сотруднику.", HandOff: true, Reason: conversations.HandoffAsked})
	if err := f.deliver(m.ID, false); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	sent = f.bots.SnapshotSent()
	if last = sent[len(sent)-1]; !strings.HasPrefix(last.Text, "Передаю") || len(last.Buttons) != 0 {
		t.Fatalf("after the handoff: %+v", last)
	}
}
