package routing_test

import (
	"bytes"
	"context"
	"errors"
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

func (f *fixture) roundRobin() { f.t.Helper(); f.settings(routing.ModeRoundRobin, nil) }

func (f *fixture) settings(mode routing.Mode, maxChats *int) {
	f.t.Helper()
	if _, err := f.router.SetSettings(f.ctx, f.ws, routing.Settings{Mode: mode, MaxChats: maxChats}); err != nil {
		f.t.Fatalf("set settings: %v", err)
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
// wantOnline checks what the widget is told (W-02).
func (f *fixture) wantOnline(want bool) {
	f.t.Helper()
	if got, err := f.router.AnyOnline(f.ctx, f.ws); err != nil || got != want {
		f.t.Fatalf("any online = %v, %v; want %v", got, err, want)
	}
}

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
	f.wantOnline(false)

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
	f.wantOnline(true)

	if _, err := f.router.SetStatus(f.ctx, f.ws, a, routing.StatusAway); err != nil {
		t.Fatalf("away: %v", err)
	}
	if p, _ := f.router.Presences(f.ctx, f.ws); p[a] != routing.PresenceAway {
		t.Fatalf("presence %q", p[a])
	}
	f.wantOnline(false)
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
	f.wantOnline(false)
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
	if _, err := f.router.SetSettings(f.ctx, f.ws, routing.Settings{Mode: "random"}); err == nil {
		t.Fatal("unknown mode accepted")
	}
	for _, n := range []int{0, 51} {
		if _, err := f.router.SetSettings(f.ctx, f.ws, routing.Settings{Mode: routing.ModeRoundRobin, MaxChats: &n}); err == nil {
			t.Fatalf("limit %d accepted", n)
		}
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
	f.settings(routing.ModeManual, nil)
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

// A conversation another transaction holds while the router runs, such as
// one taking the customer's message, is not left waiting: the run comes
// back for it.
func TestDispatch_ComesBackForLocked(t *testing.T) {
	f := setup(t)
	a := f.operator("anna")
	f.roundRobin()
	f.seen(a)
	held := f.conversation(0)
	f.forgetJobs()

	conn, err := pgx.Connect(f.ctx, f.ownerURL)
	if err != nil {
		t.Fatalf("connect owner: %v", err)
	}
	defer func() { _ = conn.Close(f.ctx) }()
	tx, err := conn.Begin(f.ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.Exec(f.ctx, "SELECT 1 FROM conversations WHERE id = $1 FOR UPDATE", held); err != nil {
		t.Fatalf("lock: %v", err)
	}
	if n := f.dispatch(); n != 0 {
		t.Fatalf("assigned %d while the row was held", n)
	}
	if err := tx.Rollback(f.ctx); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	var later int
	if err := conn.QueryRow(f.ctx,
		"SELECT count(*) FROM river.river_job WHERE kind = 'routing.dispatch' AND state = 'scheduled' AND scheduled_at > now()",
	).Scan(&later); err != nil {
		t.Fatalf("count jobs: %v", err)
	}
	if later != 1 {
		t.Fatalf("%d runs scheduled for the held conversation, want 1", later)
	}
	if n := f.dispatch(); n != 1 || f.assigneeOf(held) != a {
		t.Fatalf("assigned %d after the row was released", n)
	}
	// With nothing left in the queue no run comes back.
	f.forgetJobs()
	f.dispatch()
	var left int
	if err := conn.QueryRow(f.ctx, "SELECT count(*) FROM river.river_job WHERE kind = 'routing.dispatch'").Scan(&left); err != nil || left != 0 {
		t.Fatalf("an empty queue queued %d runs (%v)", left, err)
	}
}

func ptr(n int) *int { return &n }

// Nobody gets more than their capacity; the rest waits and goes out as
// soon as a conversation leaves someone's work.
func TestDispatch_Capacity(t *testing.T) {
	f := setup(t)
	a, b := f.operator("anna"), f.operator("boris")
	f.settings(routing.ModeRoundRobin, ptr(2))
	if err := f.router.SetLimit(f.ctx, f.ws, b, ptr(1)); err != nil {
		t.Fatalf("own limit: %v", err)
	}
	f.seen(a, b)
	ids := []uuid.UUID{f.conversation(0), f.conversation(0), f.conversation(0), f.conversation(0)}
	if n := f.dispatch(); n != 3 {
		t.Fatalf("assigned %d, want 3: two to anna, one to boris", n)
	}
	count := map[uuid.UUID]int{}
	for _, id := range ids[:3] {
		count[f.assigneeOf(id)]++
	}
	if count[a] != 2 || count[b] != 1 {
		t.Fatalf("loads %v", count)
	}
	if c := f.get(ids[3]); c.Status != conversations.StatusQueued {
		t.Fatalf("fourth: %s", c.Status)
	}
	// Full members are no reason to come back in 2 s.
	f.forgetJobs()
	f.dispatch()
	if n := f.dispatchJobs(); n != 0 {
		t.Fatalf("a full team queued %d runs", n)
	}

	limits, err := f.router.Limits(f.ctx, f.ws, f.convs.Loads)
	if err != nil {
		t.Fatalf("limits: %v", err)
	}
	byUser := map[uuid.UUID]routing.Limit{}
	for _, l := range limits {
		byUser[l.UserID] = l
	}
	if l := byUser[b]; l.MaxChats == nil || *l.MaxChats != 1 || l.Open != 1 {
		t.Fatalf("boris: %+v", l)
	}
	if l := byUser[a]; l.MaxChats != nil || l.Open != 2 {
		t.Fatalf("anna: %+v", l)
	}
	if _, ok := byUser[f.owner]; !ok {
		t.Fatal("the owner answers customers too")
	}

	// Boris resolves his: room again, and the waiting one goes to him.
	var his uuid.UUID
	for _, id := range ids[:3] {
		if f.assigneeOf(id) == b {
			his = id
		}
	}
	if _, err := f.convs.Transition(f.ctx, f.ws, conversations.Actor{UserID: b}, his, conversations.StatusResolved, nil); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if f.dispatchJobs() == 0 {
		t.Fatal("a freed place queued no dispatch")
	}
	f.dispatch()
	if got := f.assigneeOf(ids[3]); got != b {
		t.Fatalf("waiting one went to %v, want boris", got)
	}

	// Without limits a resolved conversation frees nothing anyone waits for.
	f.settings(routing.ModeRoundRobin, nil)
	if err := f.router.SetLimit(f.ctx, f.ws, b, nil); err != nil {
		t.Fatalf("drop own limit: %v", err)
	}
	f.forgetJobs()
	if _, err := f.convs.Transition(f.ctx, f.ws, conversations.Actor{UserID: b}, ids[3], conversations.StatusResolved, nil); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if n := f.dispatchJobs(); n != 0 {
		t.Fatalf("resolving without limits queued %d runs", n)
	}

	if err := f.router.SetLimit(f.ctx, f.ws, uuid.Must(uuid.NewV7()), ptr(1)); !errors.Is(err, routing.ErrNotFound) {
		t.Fatalf("a stranger's limit: %v", err)
	}
}

// least_loaded gives each conversation to whoever has the smallest share of
// their capacity in use; equals in turn.
func TestDispatch_LeastLoaded(t *testing.T) {
	f := setup(t)
	a, b := f.operator("anna"), f.operator("boris")
	f.settings(routing.ModeRoundRobin, nil)
	if err := f.router.SetLimit(f.ctx, f.ws, a, ptr(4)); err != nil {
		t.Fatalf("own limit: %v", err)
	}
	if err := f.router.SetLimit(f.ctx, f.ws, b, ptr(2)); err != nil {
		t.Fatalf("own limit: %v", err)
	}
	// Anna already has two by hand: half full; boris is empty.
	for range 2 {
		id := f.conversation(0)
		if _, err := f.convs.Assign(f.ctx, f.ws, conversations.Actor{UserID: f.owner, AnyConversation: true, AssignOthers: true}, id, &a, nil); err != nil {
			t.Fatalf("assign: %v", err)
		}
	}
	f.settings(routing.ModeLeastLoaded, nil)
	f.seen(a, b)
	first := f.conversation(0)
	f.dispatch()
	if got := f.assigneeOf(first); got != b {
		t.Fatalf("first went to %v, want boris (0/2 against 2/4)", got)
	}
	// Now both are half full: the next goes to whoever is first in turn,
	// that is anna, who got nothing from the router yet.
	second := f.conversation(0)
	f.dispatch()
	if got := f.assigneeOf(second); got != a {
		t.Fatalf("second went to %v, want anna", got)
	}
}
