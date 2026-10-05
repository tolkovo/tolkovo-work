package routing_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tolkovo/tolkovo/apps/server/internal/conversations"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/db"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/dbtest"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/jobs"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/storage"
	"github.com/tolkovo/tolkovo/apps/server/internal/routing"
	"github.com/tolkovo/tolkovo/apps/server/internal/tenancy"
)

// fixture is a fresh database with one workspace, its owner, the
// conversations module and the router wired as cmd wires them.
type fixture struct {
	t        *testing.T
	ctx      context.Context
	app      *db.DB
	ownerURL string
	tenants  *tenancy.Service
	convs    *conversations.Service
	router   *routing.Service
	ws       uuid.UUID
	owner    uuid.UUID
}

func setup(t *testing.T) *fixture {
	t.Helper()
	d := dbtest.New(t)
	app := d.OpenApp(t)
	files, err := storage.NewFS(storage.FSOptions{
		Root: t.TempDir(), LinkBaseURL: "http://localhost/api/v1/files",
		SigningKey: bytes.Repeat([]byte("k"), storage.MinSigningKeyLen),
	})
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	t.Cleanup(func() { _ = files.Close() })
	// Insert-only, as in the api role: the test runs Dispatch itself.
	inserter, err := jobs.New(app.Pool(), jobs.Options{Log: slog.New(slog.NewJSONHandler(io.Discard, nil))}) //nolint:forbidigo // the queue is platform infrastructure
	if err != nil {
		t.Fatalf("jobs: %v", err)
	}
	f := &fixture{
		t: t, ctx: context.Background(), app: app, ownerURL: d.OwnerURL, tenants: tenancy.New(app),
		convs: conversations.New(app, files), router: routing.New(app, inserter),
	}
	f.convs.SetRouter(f.router)
	f.owner = f.user("owner")
	ws, err := f.tenants.CreateWorkspace(f.ctx, tenancy.CreateWorkspaceInput{Name: "Зерно", Slug: "zerno", OwnerID: f.owner})
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	f.ws = ws.ID
	return f
}

func (f *fixture) user(name string) uuid.UUID {
	f.t.Helper()
	id := uuid.Must(uuid.NewV7())
	err := f.app.InGlobalTx(f.ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(f.ctx, "INSERT INTO users (id, email, name) VALUES ($1, $2, $3)", id, name+"@example.test", name)
		return err
	})
	if err != nil {
		f.t.Fatalf("create user: %v", err)
	}
	return id
}

func (f *fixture) operator(name string) uuid.UUID {
	f.t.Helper()
	id := f.user(name)
	if err := f.tenants.AddMember(f.ctx, f.ws, id, tenancy.RoleOperator); err != nil {
		f.t.Fatalf("add member: %v", err)
	}
	return id
}

func (f *fixture) roundRobin() {
	f.t.Helper()
	if _, err := f.router.SetMode(f.ctx, f.ws, routing.ModeRoundRobin); err != nil {
		f.t.Fatalf("set mode: %v", err)
	}
}

func (f *fixture) seen(users ...uuid.UUID) {
	f.t.Helper()
	if err := f.router.Seen(f.ctx, f.ws, users); err != nil {
		f.t.Fatalf("seen: %v", err)
	}
}

func (f *fixture) conversation(priority int) uuid.UUID {
	f.t.Helper()
	ct, err := f.convs.CreateContact(f.ctx, f.ws, conversations.ContactInput{Name: "Клиент"})
	if err != nil {
		f.t.Fatalf("create contact: %v", err)
	}
	c, err := f.convs.CreateConversation(f.ctx, f.ws, conversations.CreateConversationInput{ContactID: ct.ID, Priority: priority})
	if err != nil {
		f.t.Fatalf("create conversation: %v", err)
	}
	return c.ID
}

func (f *fixture) dispatch() int {
	f.t.Helper()
	n, err := f.router.Dispatch(f.ctx, f.convs, f.ws)
	if err != nil {
		f.t.Fatalf("dispatch: %v", err)
	}
	return n
}

func (f *fixture) get(id uuid.UUID) conversations.Conversation {
	f.t.Helper()
	c, err := f.convs.Conversation(f.ctx, f.ws, f.owner, id)
	if err != nil {
		f.t.Fatalf("get conversation: %v", err)
	}
	return c
}

func (f *fixture) assigneeOf(id uuid.UUID) uuid.UUID {
	f.t.Helper()
	c := f.get(id)
	if c.AssigneeID == nil {
		f.t.Fatalf("conversation %d is not assigned: %s", c.Number, c.Status)
	}
	return *c.AssigneeID
}

// ownerExec changes data behind the services' back (fixtures).
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

// dispatchJobs counts routing.dispatch jobs waiting for the worker.
func (f *fixture) dispatchJobs() int {
	f.t.Helper()
	conn, err := pgx.Connect(f.ctx, f.ownerURL)
	if err != nil {
		f.t.Fatalf("connect owner: %v", err)
	}
	defer func() { _ = conn.Close(f.ctx) }()
	var n int
	if err := conn.QueryRow(f.ctx, "SELECT count(*) FROM river.river_job WHERE kind = 'routing.dispatch' AND state = 'available'").Scan(&n); err != nil {
		f.t.Fatalf("count jobs: %v", err)
	}
	return n
}

func (f *fixture) forgetJobs() {
	f.t.Helper()
	f.ownerExec("DELETE FROM river.river_job WHERE kind = 'routing.dispatch'")
}

// Conversations go in turn to members who accept them and have the panel
// open: higher priority first, then the oldest; nobody away or offline.
func TestDispatch_RoundRobin(t *testing.T) {
	f := setup(t)
	a, b := f.operator("anna"), f.operator("boris")
	away, closed := f.operator("away"), f.operator("closed")
	f.roundRobin()
	f.seen(a, b, away)
	if _, err := f.router.SetStatus(f.ctx, f.ws, away, routing.StatusAway); err != nil {
		t.Fatalf("set status: %v", err)
	}
	_ = closed // never seen: the panel is not open

	first, second := f.conversation(0), f.conversation(0)
	urgent := f.conversation(2)
	if n := f.dispatch(); n != 3 {
		t.Fatalf("assigned %d, want 3", n)
	}
	turn := []uuid.UUID{a, b}
	slices.SortFunc(turn, func(x, y uuid.UUID) int { return bytes.Compare(x[:], y[:]) })
	if got := []uuid.UUID{f.assigneeOf(urgent), f.assigneeOf(first), f.assigneeOf(second)}; !slices.Equal(got, []uuid.UUID{turn[0], turn[1], turn[0]}) {
		t.Fatalf("assignees %v, want urgent first and in turn %v", got, turn)
	}
	c := f.get(first)
	if c.Status != conversations.StatusAssigned {
		t.Fatalf("status %s", c.Status)
	}

	// The next one goes to whoever did not get the last.
	next := f.conversation(0)
	if f.dispatchJobs() == 0 {
		t.Fatal("a new conversation queued no dispatch")
	}
	f.dispatch()
	if got := f.assigneeOf(next); got != turn[1] {
		t.Fatalf("next went to %v, want %v", got, turn[1])
	}

	// The system line says nobody did it by hand.
	page, err := f.convs.Messages(f.ctx, f.ws, next, "", 10)
	if err != nil {
		t.Fatalf("messages: %v", err)
	}
	var line *conversations.Message
	for i := range page.Items {
		if page.Items[i].Kind == conversations.KindEvent {
			line = &page.Items[i]
		}
	}
	if line == nil || line.SenderID != nil || line.Event == nil || line.Event.Type != conversations.EventAssigned {
		t.Fatalf("system line: %+v", line)
	}
}

// Manual distribution leaves the queue alone and queues no jobs.
func TestDispatch_Manual(t *testing.T) {
	f := setup(t)
	a := f.operator("anna")
	f.seen(a)
	f.forgetJobs()
	id := f.conversation(0)
	if n := f.dispatchJobs(); n != 0 {
		t.Fatalf("jobs in manual mode: %d", n)
	}
	if n := f.dispatch(); n != 0 {
		t.Fatalf("assigned %d in manual mode", n)
	}
	if c := f.get(id); c.Status != conversations.StatusQueued {
		t.Fatalf("status %s", c.Status)
	}

	// Switching to round_robin distributes what waits.
	f.roundRobin()
	if f.dispatchJobs() == 0 {
		t.Fatal("switching on queued no dispatch")
	}
	f.dispatch()
	if got := f.assigneeOf(id); got != a {
		t.Fatalf("assignee %v", got)
	}
}

// A member who gives a conversation back goes to the end of the turn.
func TestDispatch_ReleasedGoesToAnother(t *testing.T) {
	f := setup(t)
	a, b := f.operator("anna"), f.operator("boris")
	f.roundRobin()
	f.seen(a, b)
	c1, c2 := f.conversation(0), f.conversation(0)
	f.dispatch()
	first := f.assigneeOf(c1)
	other := f.assigneeOf(c2)
	// first is next in turn; giving c1 back moves them behind other.
	got, err := f.convs.Assign(f.ctx, f.ws, conversations.Actor{UserID: first}, c1, nil, nil)
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	if got.Status != conversations.StatusQueued {
		t.Fatalf("released status %s", got.Status)
	}
	f.dispatch()
	if got := f.assigneeOf(c1); got != other {
		t.Fatalf("released conversation went back to %v, want %v", got, other)
	}
}

// Presence: online while seen, away by choice, offline once the panel is
// closed for SeenTimeout. Coming back queues a dispatch.
func TestPresence(t *testing.T) {
	f := setup(t)
	a := f.operator("anna")
	f.roundRobin()

	if st, err := f.router.Status(f.ctx, f.ws, a); err != nil || st != routing.StatusOnline {
		t.Fatalf("default status %q %v", st, err)
	}
	p, err := f.router.Presences(f.ctx, f.ws)
	if err != nil {
		t.Fatalf("presences: %v", err)
	}
	if _, ok := p[a]; ok {
		t.Fatalf("never seen but present: %v", p)
	}

	f.forgetJobs()
	f.seen(a)
	if f.dispatchJobs() != 1 {
		t.Fatal("first heartbeat queued no dispatch")
	}
	f.forgetJobs()
	f.seen(a)
	if n := f.dispatchJobs(); n != 0 {
		t.Fatalf("a heartbeat of someone online queued %d dispatches", n)
	}
	if p, _ := f.router.Presences(f.ctx, f.ws); p[a] != routing.PresenceOnline {
		t.Fatalf("presence %q", p[a])
	}

	if _, err := f.router.SetStatus(f.ctx, f.ws, a, routing.StatusAway); err != nil {
		t.Fatalf("away: %v", err)
	}
	if p, _ := f.router.Presences(f.ctx, f.ws); p[a] != routing.PresenceAway {
		t.Fatalf("presence %q", p[a])
	}
	id := f.conversation(0)
	if n := f.dispatch(); n != 0 {
		t.Fatalf("assigned %d to someone away", n)
	}
	f.forgetJobs()
	if _, err := f.router.SetStatus(f.ctx, f.ws, a, routing.StatusOnline); err != nil {
		t.Fatalf("online: %v", err)
	}
	if f.dispatchJobs() != 1 {
		t.Fatal("coming back online queued no dispatch")
	}

	// The panel closed long ago: offline, nothing assigned.
	f.ownerExec("UPDATE operator_states SET seen_at = now() - interval '2 minutes' WHERE user_id = $1", a)
	if p, _ := f.router.Presences(f.ctx, f.ws); p[a] != routing.PresenceOffline {
		t.Fatalf("presence %q", p[a])
	}
	if n := f.dispatch(); n != 0 {
		t.Fatalf("assigned %d to someone offline", n)
	}
	f.forgetJobs()
	f.seen(a)
	if f.dispatchJobs() != 1 {
		t.Fatal("reopening the panel queued no dispatch")
	}
	f.dispatch()
	if got := f.assigneeOf(id); got != a {
		t.Fatalf("assignee %v", got)
	}

	if _, err := f.router.SetStatus(f.ctx, f.ws, a, "busy"); err == nil {
		t.Fatal("unknown status accepted")
	}
	if _, err := f.router.SetMode(f.ctx, f.ws, "random"); err == nil {
		t.Fatal("unknown mode accepted")
	}
}

// A resolved conversation the customer reopens goes back to its operator
// only while they are available; otherwise the router finds someone.
func TestReopenWhileAway(t *testing.T) {
	f := setup(t)
	a, b := f.operator("anna"), f.operator("boris")
	ch := uuid.Must(uuid.NewV7())
	f.ownerExec("INSERT INTO channels (id, workspace_id, type, name, status) VALUES ($1, $2, 'telegram_bot', 'Бот', 'active')", ch, f.ws)
	in := func(id, body string) conversations.Inbound {
		return conversations.Inbound{
			ChannelID: ch, Thread: "70001", ExternalID: id, Body: body,
			Sender: conversations.InboundSender{ExternalUserID: "70001", Name: "Пётр"},
		}
	}
	f.roundRobin()
	f.seen(a)
	res, err := f.convs.Receive(f.ctx, f.ws, in("1", "Здравствуйте"))
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	f.dispatch()
	if got := f.assigneeOf(res.ConversationID); got != a {
		t.Fatalf("assignee %v", got)
	}
	resolve := func() {
		t.Helper()
		if _, err := f.convs.Transition(f.ctx, f.ws, conversations.Actor{UserID: a}, res.ConversationID, conversations.StatusResolved, nil); err != nil {
			t.Fatalf("resolve: %v", err)
		}
	}

	// Still online: back to anna.
	resolve()
	if _, err := f.convs.Receive(f.ctx, f.ws, in("2", "Ещё вопрос")); err != nil {
		t.Fatalf("receive: %v", err)
	}
	if c := f.get(res.ConversationID); c.Status != conversations.StatusAssigned || *c.AssigneeID != a {
		t.Fatalf("reopened while online: %s %v", c.Status, c.AssigneeID)
	}

	// Anna closed the panel; boris is here.
	resolve()
	f.ownerExec("UPDATE operator_states SET seen_at = now() - interval '2 minutes' WHERE user_id = $1", a)
	f.seen(b)
	if _, err := f.convs.Receive(f.ctx, f.ws, in("3", "Алло?")); err != nil {
		t.Fatalf("receive: %v", err)
	}
	if c := f.get(res.ConversationID); c.Status != conversations.StatusQueued {
		t.Fatalf("reopened while offline: %s", c.Status)
	}
	f.dispatch()
	if got := f.assigneeOf(res.ConversationID); got != b {
		t.Fatalf("assignee %v, want boris", got)
	}

	// Manual distribution keeps the old rule: the member gets it back.
	if _, err := f.router.SetMode(f.ctx, f.ws, routing.ModeManual); err != nil {
		t.Fatalf("manual: %v", err)
	}
	if _, err := f.convs.Transition(f.ctx, f.ws, conversations.Actor{UserID: b}, res.ConversationID, conversations.StatusResolved, nil); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	f.ownerExec("UPDATE operator_states SET seen_at = now() - interval '2 minutes' WHERE user_id = $1", b)
	if _, err := f.convs.Receive(f.ctx, f.ws, in("4", "Спасибо")); err != nil {
		t.Fatalf("receive: %v", err)
	}
	if c := f.get(res.ConversationID); c.Status != conversations.StatusAssigned || *c.AssigneeID != b {
		t.Fatalf("manual reopen: %s %v", c.Status, c.AssigneeID)
	}
}

// A deactivated member gets nothing, even with a fresh heartbeat.
func TestDispatch_SkipsInactive(t *testing.T) {
	f := setup(t)
	a := f.operator("anna")
	f.roundRobin()
	f.seen(a)
	f.ownerExec("UPDATE memberships SET is_active = false WHERE user_id = $1", a)
	f.conversation(0)
	if n := f.dispatch(); n != 0 {
		t.Fatalf("assigned %d to an inactive member", n)
	}
}
