package channels

import (
	"strings"
	"testing"

	"github.com/tolkovo/tolkovo/apps/server/internal/channels/channelstest"
	"github.com/tolkovo/tolkovo/apps/server/internal/conversations"
)

func TestBotWebhook_Topics(t *testing.T) {
	f := setup(t)
	ch, err := f.svc.ConnectBot(f.ctx, f.wsA, f.ownerA, channelstest.BotToken, "")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	secret := f.bots.Hooks[0].Secret
	crm, err := f.inbox.CreateTopic(f.ctx, f.wsA, conversations.TopicInput{Name: "CRM", Priority: 2})
	if err != nil {
		t.Fatalf("topic: %v", err)
	}
	if _, err := f.inbox.CreateTopic(f.ctx, f.wsA, conversations.TopicInput{Name: "OnlyFans"}); err != nil {
		t.Fatalf("topic: %v", err)
	}
	hook := func(u string) {
		t.Helper()
		if err := f.svc.BotWebhook(f.ctx, f.wsA, ch.ID, secret, []byte(u)); err != nil {
			t.Fatalf("webhook: %v", err)
		}
	}

	// The first message brings the buttons, one topic per row; the second
	// does not ask again.
	hook(updText)
	hook(strings.Replace(strings.Replace(updText, `"message_id":7`, `"message_id":12`, 1), `"update_id":100`, `"update_id":105`, 1))
	sent := f.bots.SnapshotSent()
	if len(sent) != 1 || sent[0].ChatID != 70001 || !strings.HasPrefix(sent[0].Text, "Выберите тему вопроса") ||
		len(sent[0].Buttons) != 2 || sent[0].Buttons[0][0].Text != "CRM" || sent[0].Buttons[0][0].Data != "topic:"+crm.ID.String() {
		t.Fatalf("buttons: %+v", sent)
	}

	// A press picks the topic and replaces the buttons with it.
	press := `{"update_id":106,"callback_query":{"id":"cb1","from":{"id":70001,"is_bot":false,"first_name":"Пётр"},` +
		`"message":{"message_id":1001,"chat":{"id":70001,"type":"private"},"date":2,"text":"…"},"data":"topic:` + crm.ID.String() + `"}}`
	hook(press)
	answers, edits := f.bots.SnapshotAnswers()
	if len(answers) != 1 || answers[0].CallbackID != "cb1" || answers[0].Text != "" {
		t.Fatalf("answers: %+v", answers)
	}
	if len(edits) != 1 || edits[0].MessageID != 1001 || edits[0].Text != "Тема вопроса: CRM" {
		t.Fatalf("edits: %+v", edits)
	}
	convs := f.inboxOf(f.wsA)
	if len(convs) != 1 || convs[0].TopicID == nil || *convs[0].TopicID != crm.ID || convs[0].Priority != 2 {
		t.Fatalf("conversation: %+v", convs)
	}

	// A button of a deleted topic, or someone else's data, gets a short answer.
	if err := f.inbox.DeleteTopic(f.ctx, f.wsA, crm.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	hook(strings.Replace(strings.Replace(press, `"update_id":106`, `"update_id":107`, 1), `"id":"cb1"`, `"id":"cb2"`, 1))
	hook(`{"update_id":108,"callback_query":{"id":"cb3","from":{"id":70001,"is_bot":false,"first_name":"Пётр"},` +
		`"message":{"message_id":1001,"chat":{"id":70001,"type":"private"},"date":2,"text":"…"},"data":"other"}}`)
	answers, edits = f.bots.SnapshotAnswers()
	if len(answers) != 3 || !strings.HasPrefix(answers[1].Text, "Эта тема больше недоступна") || answers[2].Text != "" || len(edits) != 1 {
		t.Fatalf("after delete: %+v %+v", answers, edits)
	}
}
