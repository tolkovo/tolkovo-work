package telegram

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	gotd "github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"github.com/gotd/td/tgtest"
	"github.com/gotd/td/tgtest/cluster"
	"github.com/gotd/td/transport"
)

// TestUserAuth_LoginAcrossConnections runs the login against an in-process
// MTProto server (gotd's tgtest): every step opens its own connection with
// the session the previous step returned, as the API does between requests.
func TestUserAuth_LoginAcrossConnections(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	c := cluster.NewCluster(cluster.Options{Protocol: transport.Intermediate})
	var (
		mu   sync.Mutex
		keys = map[[8]byte]bool{}
		got  []string
	)
	seen := func(req *tgtest.Request, what string) {
		mu.Lock()
		defer mu.Unlock()
		keys[req.Session.AuthKey.ID] = true
		got = append(got, what)
	}
	d := c.Dispatch(2, "dc2")
	d.HandleFunc(tg.AuthSendCodeRequestTypeID, func(s *tgtest.Server, req *tgtest.Request) error {
		var r tg.AuthSendCodeRequest
		if err := r.Decode(req.Buf); err != nil {
			return err
		}
		seen(req, "sendCode "+r.PhoneNumber)
		return s.SendResult(req, &tg.AuthSentCode{Type: &tg.AuthSentCodeTypeApp{Length: 5}, PhoneCodeHash: "hash-1"})
	})
	d.HandleFunc(tg.AuthSignInRequestTypeID, func(s *tgtest.Server, req *tgtest.Request) error {
		var r tg.AuthSignInRequest
		if err := r.Decode(req.Buf); err != nil {
			return err
		}
		seen(req, "signIn "+r.PhoneCode)
		if r.PhoneCode != "12345" || r.PhoneCodeHash != "hash-1" {
			return s.SendErr(req, tgerr.New(400, "PHONE_CODE_INVALID"))
		}
		return s.SendResult(req, &tg.AuthAuthorization{
			User: &tg.User{ID: 42, FirstName: "Марина", LastName: "Ковалёва", Username: "marina_zerno"},
		})
	})
	d.HandleFunc(tg.AuthLogOutRequestTypeID, func(s *tgtest.Server, req *tgtest.Request) error {
		seen(req, "logOut")
		return s.SendResult(req, &tg.AuthLoggedOut{})
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
	const phone = "+79001234567"
	sess, sent, err := u.SendCode(ctx, nil, phone)
	if err != nil || sent != (SentCode{Hash: "hash-1", Via: CodeViaApp}) || len(sess) == 0 {
		t.Fatalf("sendCode: %+v %d %v", sent, len(sess), err)
	}
	if _, _, err := u.SignIn(ctx, sess, phone, sent.Hash, "00000"); !errors.Is(err, ErrCodeInvalid) {
		t.Fatalf("wrong code: %v", err)
	}
	sess, acc, err := u.SignIn(ctx, sess, phone, sent.Hash, "12345")
	if err != nil || acc != (Account{ID: 42, FirstName: "Марина", LastName: "Ковалёва", Username: "marina_zerno"}) {
		t.Fatalf("signIn: %+v %v", acc, err)
	}
	if err := u.LogOut(ctx, sess); err != nil {
		t.Fatalf("logOut: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	want := []string{"sendCode " + phone, "signIn 00000", "signIn 12345", "logOut"}
	if len(got) != len(want) {
		t.Fatalf("calls: %q", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("calls: %q", got)
		}
	}
	// One auth key throughout: the stored session carried it between connections.
	if len(keys) != 1 {
		t.Fatalf("auth keys: %d", len(keys))
	}
}
