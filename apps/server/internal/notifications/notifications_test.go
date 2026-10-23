package notifications

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/tolkovo/tolkovo/apps/server/internal/conversations"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/db"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/dbtest"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/jobs"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/mail/mailtest"
	"github.com/tolkovo/tolkovo/apps/server/internal/tenancy"
)

// fixture: a fresh database (application role, RLS on), an insert-only
// queue, two workspaces and the email worker with a recording sender.
type fixture struct {
	t        *testing.T
	ctx      context.Context
	app      *db.DB
	ownerURL string
	jobs     *jobs.Client
	svc      *Service
	convs    *conversations.Service
	tenants  *tenancy.Service
	sender   *mailtest.Sender
	worker   *missedEmailWorker
	wsA, wsB uuid.UUID
	ownerA   uuid.UUID
}

func setup(t *testing.T) *fixture {
	t.Helper()
	d := dbtest.New(t)
	app := d.OpenApp(t)
	quiet := slog.New(slog.NewJSONHandler(io.Discard, nil))
	jc, err := jobs.New(app.Pool(), jobs.Options{Log: quiet}) //nolint:forbidigo // the queue is platform infrastructure, as in cmd
	if err != nil {
		t.Fatalf("jobs: %v", err)
	}
	f := &fixture{
		t: t, ctx: context.Background(), app: app, ownerURL: d.OwnerURL, jobs: jc,
		svc: New(app, jc), convs: conversations.New(app, nil), tenants: tenancy.New(app), sender: &mailtest.Sender{},
	}
	f.convs.SetNotifier(f.svc)
	f.worker = &missedEmailWorker{
		opts:  WorkerOptions{Service: f.svc, Tenancy: f.tenants, Conversations: f.convs, Sender: f.sender, PanelURL: "http://panel.test"},
		panel: "http://panel.test",
	}
	f.ownerA = f.user("anna", true)
	f.wsA = f.workspace("alpha", f.ownerA)
	f.wsB = f.workspace("bravo", f.user("boris", true))
	return f
}

func (f *fixture) user(name string, verified bool) uuid.UUID {
	f.t.Helper()
	id := uuid.Must(uuid.NewV7())
	var at *time.Time
	if verified {
		now := time.Now()
		at = &now
	}
	err := f.app.InGlobalTx(f.ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(f.ctx, "INSERT INTO users (id, email, name, email_verified_at) VALUES ($1, $2, $3, $4)",
			id, name+"@example.test", name, at)
		return err
	})
	if err != nil {
		f.t.Fatalf("create user: %v", err)
	}
	return id
}

func (f *fixture) workspace(slug string, owner uuid.UUID) uuid.UUID {
	f.t.Helper()
	ws, err := f.tenants.CreateWorkspace(f.ctx, tenancy.CreateWorkspaceInput{Name: slug, Slug: slug, OwnerID: owner})
	if err != nil {
		f.t.Fatalf("create workspace: %v", err)
	}
	return ws.ID
}

func (f *fixture) member(ws uuid.UUID, name string, verified bool) uuid.UUID {
	f.t.Helper()
	id := f.user(name, verified)
	if err := f.tenants.AddMember(f.ctx, ws, id, tenancy.RoleOperator); err != nil {
		f.t.Fatalf("add member: %v", err)
	}
	return id
}

// conversation is a queued conversation of ws with one unread customer message.
func (f *fixture) conversation(ws uuid.UUID) conversations.Conversation {
	f.t.Helper()
	k, err := f.convs.CreateContact(f.ctx, ws, conversations.ContactInput{Name: "Client"})
	if err != nil {
		f.t.Fatalf("contact: %v", err)
	}
	c, err := f.convs.CreateConversation(f.ctx, ws, conversations.CreateConversationInput{ContactID: k.ID})
	if err != nil {
		f.t.Fatalf("conversation: %v", err)
	}
	f.inbound(ws, c.ID, k.ID)
	return c
}

// inbound adds a customer message the way a channel will (T-02).
func (f *fixture) inbound(ws, conv, contact uuid.UUID) {
	f.t.Helper()
	f.ownerExec(`INSERT INTO messages (id, workspace_id, conversation_id, direction, sender_type, sender_id, kind, body)
		VALUES ($1, $2, $3, 'in', 'contact', $4, 'text', 'hello')`, uuid.Must(uuid.NewV7()), ws, conv, contact)
	f.ownerExec(`UPDATE conversations SET last_message_at = clock_timestamp() WHERE workspace_id = $1 AND id = $2`, ws, conv)
}

func (f *fixture) ownerExec(sql string, args ...any) {
	f.t.Helper()
	conn, err := pgx.Connect(f.ctx, f.ownerURL)
	if err != nil {
		f.t.Fatalf("connect owner: %v", err)
	}
	defer func() { _ = conn.Close(f.ctx) }()
	if _, err := conn.Exec(f.ctx, sql, args...); err != nil {
		f.t.Fatalf("exec %q: %v", sql, err)
	}
}

func (f *fixture) assign(ws uuid.UUID, conv, to uuid.UUID) {
	f.t.Helper()
	actor := conversations.Actor{UserID: f.ownerA, AnyConversation: true, AssignOthers: true}
	if _, err := f.convs.Assign(f.ctx, ws, actor, conv, &to, nil); err != nil {
		f.t.Fatalf("assign: %v", err)
	}
}

// queued lists the waiting email jobs.
func (f *fixture) queued() []*rivertype.JobRow {
	f.t.Helper()
	res, err := f.jobs.JobList(f.ctx, river.NewJobListParams().Kinds(MissedEmailArgs{}.Kind()).
		States(rivertype.JobStateScheduled, rivertype.JobStateAvailable))
	if err != nil {
		f.t.Fatalf("list jobs: %v", err)
	}
	return res.Jobs
}

func (f *fixture) work(ws, conv, user uuid.UUID) error {
	return f.worker.Work(f.ctx, &river.Job[MissedEmailArgs]{
		JobRow: &rivertype.JobRow{}, Args: MissedEmailArgs{WorkspaceID: ws, ConversationID: conv, UserID: user},
	})
}

func TestPrefs_DefaultsUpdateAndIsolation(t *testing.T) {
	f := setup(t)
	p, err := f.svc.Prefs(f.ctx, f.wsA, f.ownerA)
	if err != nil {
		t.Fatal(err)
	}
	if p != (Prefs{EmailMissed: true, EmailDelay: 15, TgAssigned: true, TgMessages: true, TgQueue: true}) {
		t.Fatalf("defaults = %+v", p)
	}
	off, delay := false, 60
	if p, err = f.svc.UpdatePrefs(f.ctx, f.wsA, f.ownerA, PrefsPatch{EmailMissed: &off}); err != nil {
		t.Fatal(err)
	}
	if p != (Prefs{EmailMissed: false, EmailDelay: 15, TgAssigned: true, TgMessages: true, TgQueue: true}) {
		t.Fatalf("after turning off = %+v", p)
	}
	if p, err = f.svc.UpdatePrefs(f.ctx, f.wsA, f.ownerA, PrefsPatch{EmailDelay: &delay, TgQueue: &off}); err != nil {
		t.Fatal(err)
	}
	if p != (Prefs{EmailMissed: false, EmailDelay: 60, TgAssigned: true, TgMessages: true, TgQueue: false}) {
		t.Fatalf("absent field changed: %+v", p)
	}
	bad := 7
	if _, err := f.svc.UpdatePrefs(f.ctx, f.wsA, f.ownerA, PrefsPatch{EmailDelay: &bad}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("delay 7: err = %v, want ErrInvalid", err)
	}
	// Settings are per workspace: the same user elsewhere keeps the defaults.
	if err := f.tenants.AddMember(f.ctx, f.wsB, f.ownerA, tenancy.RoleOperator); err != nil {
		t.Fatal(err)
	}
	if p, err = f.svc.Prefs(f.ctx, f.wsB, f.ownerA); err != nil || !p.EmailMissed {
		t.Fatalf("other workspace = %+v, %v", p, err)
	}
	// A user who is not a member cannot get a row (the membership key).
	stranger := f.user("sam", true)
	if _, err := f.svc.UpdatePrefs(f.ctx, f.wsA, stranger, PrefsPatch{EmailMissed: &off}); err == nil {
		t.Fatal("prefs saved for a non-member")
	}
}

func TestNeedsAttention_QueuesOneDelayedEmail(t *testing.T) {
	f := setup(t)
	olga := f.member(f.wsA, "olga", true)
	c := f.conversation(f.wsA)
	before := time.Now()
	f.assign(f.wsA, c.ID, olga)

	got := f.queued()
	if len(got) != 1 {
		t.Fatalf("queued %d jobs, want 1", len(got))
	}
	if d := got[0].ScheduledAt.Sub(before); d < 14*time.Minute || d > 16*time.Minute {
		t.Fatalf("scheduled in %v, want the default 15 minutes", d)
	}
	// Taken back by anna (no email for one's own action) and given again:
	// the waiting job absorbs the second one.
	f.assign(f.wsA, c.ID, f.ownerA)
	f.assign(f.wsA, c.ID, olga)
	if n := len(f.queued()); n != 1 {
		t.Fatalf("queued %d jobs, want 1", n)
	}
	// Another conversation is another email.
	f.assign(f.wsA, f.conversation(f.wsA).ID, olga)
	if n := len(f.queued()); n != 2 {
		t.Fatalf("queued %d jobs, want 2", n)
	}

	// Turned off: nothing is queued.
	off := false
	if _, err := f.svc.UpdatePrefs(f.ctx, f.wsA, olga, PrefsPatch{EmailMissed: &off}); err != nil {
		t.Fatal(err)
	}
	f.assign(f.wsA, f.conversation(f.wsA).ID, olga)
	if n := len(f.queued()); n != 2 {
		t.Fatalf("queued %d jobs with the email off, want 2", n)
	}

	// A server without email queues nothing.
	f.convs.SetNotifier(New(f.app, nil))
	f.assign(f.wsA, f.conversation(f.wsA).ID, f.member(f.wsA, "pavel", true))
	if n := len(f.queued()); n != 2 {
		t.Fatalf("queued %d jobs without email, want 2", n)
	}
}

func TestMissedEmail_SendsOnceWhileUnread(t *testing.T) {
	f := setup(t)
	olga := f.member(f.wsA, "olga", true)
	c := f.conversation(f.wsA)
	f.assign(f.wsA, c.ID, olga)

	if err := f.work(f.wsA, c.ID, olga); err != nil {
		t.Fatalf("work: %v", err)
	}
	sent := f.sender.Sent()
	if len(sent) != 1 {
		t.Fatalf("sent %d emails, want 1", len(sent))
	}
	m := sent[0]
	if m.To != "olga@example.test" || !strings.Contains(m.Subject, "№1") {
		t.Fatalf("email to %q, subject %q", m.To, m.Subject)
	}
	for _, want := range []string{"http://panel.test/inbox/" + c.ID.String(), "http://panel.test/settings/notifications", ": 1."} {
		if !strings.Contains(m.Text, want) {
			t.Fatalf("email misses %q:\n%s", want, m.Text)
		}
	}
	// The customer's text never leaves in an email.
	if strings.Contains(m.Text, "hello") || strings.Contains(m.Text, "Client") {
		t.Fatalf("email carries the conversation content:\n%s", m.Text)
	}

	// The same unread messages are not emailed twice.
	if err := f.work(f.wsA, c.ID, olga); err != nil {
		t.Fatal(err)
	}
	if n := len(f.sender.Sent()); n != 1 {
		t.Fatalf("sent %d emails for the same messages", n)
	}
	// A new message is.
	f.inbound(f.wsA, c.ID, c.ContactID)
	if err := f.work(f.wsA, c.ID, olga); err != nil {
		t.Fatal(err)
	}
	if n := len(f.sender.Sent()); n != 2 {
		t.Fatalf("sent %d emails after a new message, want 2", n)
	}
}

func TestMissedEmail_Skips(t *testing.T) {
	f := setup(t)
	olga := f.member(f.wsA, "olga", true)
	unverified := f.member(f.wsA, "uma", false)

	cases := map[string]func() (conv, user uuid.UUID){
		"read meanwhile": func() (uuid.UUID, uuid.UUID) {
			c := f.conversation(f.wsA)
			f.assign(f.wsA, c.ID, olga)
			feed, err := f.convs.Messages(f.ctx, f.wsA, c.ID, "", 10)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.convs.MarkRead(f.ctx, f.wsA, olga, c.ID, feed.Items[0].ID); err != nil {
				t.Fatal(err)
			}
			return c.ID, olga
		},
		"given to someone else": func() (uuid.UUID, uuid.UUID) {
			c := f.conversation(f.wsA)
			f.assign(f.wsA, c.ID, olga)
			f.assign(f.wsA, c.ID, f.ownerA)
			return c.ID, olga
		},
		"resolved": func() (uuid.UUID, uuid.UUID) {
			c := f.conversation(f.wsA)
			f.assign(f.wsA, c.ID, olga)
			actor := conversations.Actor{UserID: f.ownerA, AnyConversation: true}
			if _, err := f.convs.Transition(f.ctx, f.wsA, actor, c.ID, conversations.StatusResolved, nil); err != nil {
				t.Fatal(err)
			}
			return c.ID, olga
		},
		"email not confirmed": func() (uuid.UUID, uuid.UUID) {
			c := f.conversation(f.wsA)
			f.assign(f.wsA, c.ID, unverified)
			return c.ID, unverified
		},
		"turned off meanwhile": func() (uuid.UUID, uuid.UUID) {
			c := f.conversation(f.wsA)
			f.assign(f.wsA, c.ID, olga)
			off := false
			if _, err := f.svc.UpdatePrefs(f.ctx, f.wsA, olga, PrefsPatch{EmailMissed: &off}); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				on := true
				_, _ = f.svc.UpdatePrefs(f.ctx, f.wsA, olga, PrefsPatch{EmailMissed: &on})
			})
			return c.ID, olga
		},
		"left the workspace": func() (uuid.UUID, uuid.UUID) {
			leaver := f.member(f.wsA, "leo", true)
			c := f.conversation(f.wsA)
			f.assign(f.wsA, c.ID, leaver)
			f.ownerExec(`UPDATE memberships SET is_active = false WHERE workspace_id = $1 AND user_id = $2`, f.wsA, leaver)
			return c.ID, leaver
		},
		"conversation of another workspace": func() (uuid.UUID, uuid.UUID) {
			return f.conversation(f.wsB).ID, olga
		},
	}
	for name, prepare := range cases {
		t.Run(name, func(t *testing.T) {
			conv, user := prepare()
			before := len(f.sender.Sent())
			if err := f.work(f.wsA, conv, user); err != nil {
				t.Fatalf("work: %v", err)
			}
			if n := len(f.sender.Sent()); n != before {
				t.Fatalf("an email went out")
			}
		})
	}
}
