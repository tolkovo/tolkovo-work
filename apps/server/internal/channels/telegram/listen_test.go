package telegram

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/gotd/td/proto"
	gotd "github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgtest"
	"github.com/gotd/td/tgtest/cluster"
	"github.com/gotd/td/transport"
)

// lockedState is a StateStore safe for the listener's goroutines.
type lockedState struct {
	mu sync.Mutex
	memState
}

func (l *lockedState) LoadState(ctx context.Context) (UpdateState, bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.memState.LoadState(ctx)
}

func (l *lockedState) SaveState(ctx context.Context, st UpdateState) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.memState.SaveState(ctx, st)
}

func (l *lockedState) pts() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.st == nil {
		return 0
	}
	return l.st.Pts
}

// TestUserAuth_Listen runs a personal account against an in-process MTProto
// server: the stream starts at the server's state, a pushed private message
// arrives, a group message and Saved Messages do not, and a message from an
// unknown person is fetched through updates.getDifference.
func TestUserAuth_Listen(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	c := cluster.NewCluster(cluster.Options{Protocol: transport.Intermediate})
	var (
		mu      sync.Mutex
		session *tgtest.Session
		diffs   int
	)
	now := int(time.Now().Unix())
	person := &tg.User{ID: 70001, AccessHash: 11, FirstName: "Ольга", Username: "olga_k"}
	stranger := &tg.User{ID: 70003, AccessHash: 13, FirstName: "Иван"}
	srv, d := c.DC(2, "dc2")
	d.HandleFunc(tg.UsersGetUsersRequestTypeID, func(s *tgtest.Server, req *tgtest.Request) error {
		return s.SendVector(req, &tg.User{ID: 42, Self: true, AccessHash: 1, FirstName: "Марина"})
	})
	d.HandleFunc(tg.UpdatesGetStateRequestTypeID, func(s *tgtest.Server, req *tgtest.Request) error {
		mu.Lock()
		session = &req.Session
		mu.Unlock()
		return s.SendResult(req, &tg.UpdatesState{Pts: 100, Qts: 0, Date: now, Seq: 0})
	})
	d.HandleFunc(tg.UpdatesGetDifferenceRequestTypeID, func(s *tgtest.Server, req *tgtest.Request) error {
		var r tg.UpdatesGetDifferenceRequest
		if err := r.Decode(req.Buf); err != nil {
			return err
		}
		mu.Lock()
		diffs++
		mu.Unlock()
		if r.Pts < 103 {
			return s.SendResult(req, &tg.UpdatesDifferenceEmpty{Date: now, Seq: 0})
		}
		return s.SendResult(req, &tg.UpdatesDifference{
			NewMessages: []tg.MessageClass{&tg.Message{ID: 13, PeerID: &tg.PeerUser{UserID: 70003}, FromID: &tg.PeerUser{UserID: 70003}, Message: "Через разницу", Date: now}},
			Users:       []tg.UserClass{stranger},
			State:       tg.UpdatesState{Pts: 104, Date: now},
		})
	})

	up := make(chan error, 1)
	go func() { up <- c.Up(ctx) }()
	select {
	case <-c.Ready():
	case err := <-up:
		t.Fatalf("cluster: %v", err)
	}
	u := NewUserAuth(1, "test-hash", "test")
	u.options = func(o gotd.Options) gotd.Options {
		o.PublicKeys, o.Resolver, o.DCList, o.DC = c.Keys(), c.Resolver(), c.List(), 2
		return o
	}
	// A session with an auth key, as a finished login leaves it.
	sess, err := u.run(ctx, nil, func(context.Context, *gotd.Client) error { return nil })
	if err != nil || len(sess) == 0 {
		t.Fatalf("session: %v", err)
	}

	state := &lockedState{}
	got := make(chan Message, 10)
	ready := make(chan struct{})
	lctx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- u.Listen(lctx, ListenOptions{
			Session: sess, State: state,
			Handle: func(_ context.Context, m Message, _ Fetch) error {
				got <- m
				return nil
			},
			OnReady: func() { close(ready) },
		})
	}()
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("listen: %v", err)
	}
	if state.pts() != 100 {
		t.Fatalf("the stream must start at the server's state, pts %d", state.pts())
	}
	mu.Lock()
	k := *session
	mu.Unlock()

	msg := func(id int, peer tg.PeerClass, text string) *tg.Message {
		return &tg.Message{ID: id, PeerID: peer, FromID: peer, Message: text, Date: now}
	}
	push := func(pts int, m *tg.Message, users ...tg.UserClass) {
		t.Helper()
		if err := srv.Send(ctx, k, proto.MessageFromServer, &tg.Updates{
			Updates: []tg.UpdateClass{&tg.UpdateNewMessage{Message: m, Pts: pts, PtsCount: 1}},
			Users:   users, Date: now,
		}); err != nil {
			t.Fatalf("push: %v", err)
		}
	}
	push(101, msg(10, &tg.PeerChat{ChatID: 5}, "в группе"))
	push(102, msg(11, &tg.PeerUser{UserID: 42}, "в Избранном"))
	push(103, msg(12, &tg.PeerUser{UserID: 70001}, "Добрый день"), person)

	select {
	case m := <-got:
		if m.ID != 12 || m.Text != "Добрый день" || m.From.Name() != "Ольга" || m.ChatID != 70001 {
			t.Fatalf("message %+v", m)
		}
	case err := <-done:
		t.Fatalf("listen ended: %v", err)
	case <-ctx.Done():
		t.Fatal("no message")
	}

	// From someone the account has not seen: gotd fetches the difference.
	push(104, msg(13, &tg.PeerUser{UserID: 70003}, "Через разницу"))
	select {
	case m := <-got:
		if m.ID != 13 || m.From.Name() != "Иван" {
			t.Fatalf("message through the difference %+v", m)
		}
	case <-ctx.Done():
		t.Fatal("no message through the difference")
	}
	deadline := time.Now().Add(5 * time.Second)
	for state.pts() != 104 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if state.pts() != 104 {
		t.Fatalf("saved pts %d, want 104", state.pts())
	}
	select {
	case m := <-got:
		t.Fatalf("unexpected message %+v", m)
	default:
	}

	stop()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("stop: %v", err)
	}
}
