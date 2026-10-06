package conversations_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tolkovo/tolkovo/apps/server/internal/conversations"
)

// settledSpy hears resolved and reopened conversations like the channels
// module does.
type settledSpy struct {
	got []conversations.SettledConversation
}

func (s *settledSpy) QueueDelivery(context.Context, pgx.Tx, conversations.QueuedReply) error {
	return nil
}

func (s *settledSpy) QueueSettled(_ context.Context, _ pgx.Tx, c conversations.SettledConversation) error {
	s.got = append(s.got, c)
	return nil
}

func TestRateVisitorConversation(t *testing.T) {
	f := setup(t)
	spy := &settledSpy{}
	f.svc.SetDeliverer(spy)
	ch := f.channel(f.wsA)
	res := f.receive(f.wsA, inbound(ch, "1", "Сколько стоит доставка?"))
	chat := conversations.VisitorChat{ChannelID: ch, Thread: "70001"}

	if _, err := f.svc.RateVisitorConversation(f.ctx, f.wsA, chat, res.ConversationID, 5, ""); !errors.Is(err, conversations.ErrConversationOpen) {
		t.Fatalf("open conversation: %v", err)
	}
	// Only the bot answered.
	f.ownerExec(`INSERT INTO messages (id, workspace_id, conversation_id, direction, sender_type, kind, body)
		VALUES ($1, $2, $3, 'out', 'bot', 'text', 'Бесплатно от 3000 ₽')`, uuid.Must(uuid.NewV7()), f.wsA, res.ConversationID)
	if _, err := f.svc.Assign(f.ctx, f.wsA, admin(f.ownerA), res.ConversationID, &f.ownerA, nil); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if _, err := f.svc.Transition(f.ctx, f.wsA, admin(f.ownerA), res.ConversationID, conversations.StatusResolved, nil); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(spy.got) != 1 || !spy.got[0].Resolved || spy.got[0].ConversationID != res.ConversationID || spy.got[0].Thread != "70001" {
		t.Fatalf("settled: %+v", spy.got)
	}
	// Closing a resolved conversation changes nothing the customer sees.
	if _, err := f.svc.Transition(f.ctx, f.wsA, admin(f.ownerA), res.ConversationID, conversations.StatusClosed, nil); err != nil {
		t.Fatalf("close: %v", err)
	}
	if len(spy.got) != 1 {
		t.Fatalf("settled after closing: %+v", spy.got)
	}

	r, err := f.svc.RateVisitorConversation(f.ctx, f.wsA, chat, res.ConversationID, 2, "  ")
	if err != nil {
		t.Fatalf("rate: %v", err)
	}
	if r.Score != 2 || r.Comment != nil || r.ResolvedBy != conversations.ResolvedByAI {
		t.Fatalf("rating: %+v", r)
	}
	conv, err := f.svc.Conversation(f.ctx, f.wsA, f.ownerA, res.ConversationID)
	if err != nil || conv.Rating == nil || conv.Rating.Score != 2 || conv.Rating.ResolvedBy != conversations.ResolvedByAI {
		t.Fatalf("conversation rating: %+v, %v", conv.Rating, err)
	}
	page, err := f.svc.VisitorMessages(f.ctx, f.wsA, chat, "")
	if err != nil || page.Conversation == nil || !page.Conversation.Resolved || page.Conversation.Rating == nil {
		t.Fatalf("visitor conversation: %+v, %v", page.Conversation, err)
	}

	// Another thread or workspace finds nothing.
	other := conversations.VisitorChat{ChannelID: ch, Thread: "70002"}
	if _, err := f.svc.RateVisitorConversation(f.ctx, f.wsA, other, res.ConversationID, 5, ""); !errors.Is(err, conversations.ErrNotFound) {
		t.Fatalf("another thread: %v", err)
	}
	if _, err := f.svc.RateVisitorConversation(f.ctx, f.wsB, chat, res.ConversationID, 5, ""); !errors.Is(err, conversations.ErrNotFound) {
		t.Fatalf("another workspace: %v", err)
	}
	if _, err := f.svc.RateVisitorConversation(f.ctx, f.wsA, chat, res.ConversationID, 0, ""); !errors.Is(err, conversations.ErrInvalid) {
		t.Fatalf("score 0: %v", err)
	}
}
