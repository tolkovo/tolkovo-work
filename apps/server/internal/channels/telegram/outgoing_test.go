package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

func TestBotAPI_Send(t *testing.T) {
	var (
		text, action map[string]any
		upload       struct{ method, chat, field, name, data string }
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := strings.TrimPrefix(r.URL.Path, "/bot"+token+"/")
		switch method {
		case "sendMessage", "sendChatAction":
			body := map[string]any{}
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &body)
			if method == "sendMessage" {
				text = body
				if body["chat_id"] == float64(404) {
					_, _ = io.WriteString(w, `{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`)
					return
				}
				if body["chat_id"] == float64(403) {
					_, _ = io.WriteString(w, `{"ok":false,"error_code":403,"description":"Forbidden: bot was blocked by the user"}`)
					return
				}
				if body["chat_id"] == float64(429) {
					_, _ = io.WriteString(w, `{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 5","parameters":{"retry_after":5}}`)
					return
				}
				if body["chat_id"] == float64(500) {
					_, _ = io.WriteString(w, `{"ok":false,"error_code":502,"description":"Bad Gateway"}`)
					return
				}
				_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":77}}`)
				return
			}
			action = body
			_, _ = io.WriteString(w, `{"ok":true,"result":true}`)
		case "sendPhoto", "sendDocument":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			upload.method, upload.chat = method, r.FormValue("chat_id")
			for field, files := range r.MultipartForm.File {
				f, _ := files[0].Open()
				b, _ := io.ReadAll(f)
				upload.field, upload.name, upload.data = field, files[0].Filename, string(b)
			}
			_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":78}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	api := NewBotAPI(srv.URL, srv.Client())
	ctx := context.Background()

	if id, err := api.SendMessage(ctx, token, 70001, "Здравствуйте"); err != nil || id != 77 || text["text"] != "Здравствуйте" {
		t.Fatalf("sendMessage: %d %v %v", id, err, text)
	}
	id, err := api.SendFile(ctx, token, 70001, OutFile{Name: `чек "1".jpg`, MIME: "image/jpeg", AsPhoto: true}, strings.NewReader("jpeg"))
	if err != nil || id != 78 || upload.method != "sendPhoto" || upload.field != "photo" || upload.chat != "70001" || upload.data != "jpeg" {
		t.Fatalf("sendPhoto: %d %v %+v", id, err, upload)
	}
	if upload.name != "чек _1_.jpg" {
		t.Fatalf("file name in the form: %q", upload.name)
	}
	if _, err := api.SendFile(ctx, token, 70001, OutFile{Name: "договор.pdf"}, strings.NewReader("%PDF")); err != nil || upload.method != "sendDocument" || upload.field != "document" {
		t.Fatalf("sendDocument: %v %+v", err, upload)
	}
	if err := api.SendChatAction(ctx, token, 70001); err != nil || action["action"] != "typing" {
		t.Fatalf("sendChatAction: %v %v", err, action)
	}

	var undeliverable *UndeliverableError
	if _, err := api.SendMessage(ctx, token, 403, "x"); !errors.As(err, &undeliverable) || undeliverable.Reason != ReasonBlocked {
		t.Fatalf("blocked: %v", err)
	}
	if _, err := api.SendMessage(ctx, token, 404, "x"); !errors.As(err, &undeliverable) || undeliverable.Reason != ReasonChatNotFound {
		t.Fatalf("chat not found: %v", err)
	}
	var apiErr *APIError
	if _, err := api.SendMessage(ctx, token, 429, "x"); errors.As(err, &undeliverable) || !errors.As(err, &apiErr) || apiErr.RetryAfter != 5*time.Second {
		t.Fatalf("429 stays retryable: %v", err)
	}
	if _, err := api.SendMessage(ctx, token, 500, "x"); errors.As(err, &undeliverable) || err == nil {
		t.Fatalf("a server error stays retryable: %v", err)
	}
	// Neither errors nor anything else carries the token.
	srv.Close()
	if _, err := api.SendFile(ctx, token, 1, OutFile{Name: "a"}, strings.NewReader("a")); err == nil || strings.Contains(err.Error(), token) {
		t.Fatalf("network error: %v", err)
	}
}

func TestUserSendErr(t *testing.T) {
	rpc := func(code int, typ string) error { return tgerr.New(code, typ) }
	for err, want := range map[error]string{
		rpc(400, "USER_IS_BLOCKED"):        ReasonBlocked,
		rpc(400, "PEER_ID_INVALID"):        ReasonChatNotFound,
		rpc(400, "INPUT_USER_DEACTIVATED"): ReasonChatNotFound,
		rpc(400, "PEER_FLOOD"):             ReasonLimited,
		rpc(400, "MESSAGE_TOO_LONG"):       ReasonRejected,
		rpc(403, "CHAT_WRITE_FORBIDDEN"):   ReasonRejected,
	} {
		var u *UndeliverableError
		if got := userSendErr(err); !errors.As(got, &u) || u.Reason != want {
			t.Errorf("%v: got %v, want %s", err, got, want)
		}
	}
	if got := userSendErr(rpc(400, "RANDOM_ID_DUPLICATE")); !errors.Is(got, ErrDuplicate) {
		t.Errorf("duplicate: %v", got)
	}
	var flood *FloodWaitError
	if got := userSendErr(rpc(420, "FLOOD_WAIT_30")); !errors.As(got, &flood) || flood.Wait != 30*time.Second {
		t.Errorf("flood: %v", got)
	}
	if got := userSendErr(rpc(401, "AUTH_KEY_UNREGISTERED")); !errors.Is(got, ErrSessionRevoked) {
		t.Errorf("session: %v", got)
	}
	if got := userSendErr(rpc(500, "INTERNAL")); got == nil || errors.As(got, new(*UndeliverableError)) {
		t.Errorf("server errors stay retryable: %v", got)
	}
}

func TestBlankMessages(t *testing.T) {
	msg := &tg.Message{ID: 12, Message: "Завтра до 18:00", PeerID: &tg.PeerUser{UserID: 1}}
	in := &tg.Updates{Updates: []tg.UpdateClass{
		&tg.UpdateMessageID{ID: 12, RandomID: 5},
		&tg.UpdateNewMessage{Message: msg, Pts: 7, PtsCount: 1},
	}}
	out, ok := blankMessages(in).(*tg.Updates)
	if !ok {
		t.Fatal("still updates")
	}
	nm := out.Updates[1].(*tg.UpdateNewMessage)
	if e, ok := nm.Message.(*tg.MessageEmpty); !ok || e.ID != 12 || nm.Pts != 7 || nm.PtsCount != 1 {
		t.Fatalf("blanked: %#v", nm)
	}
	if in.Updates[1].(*tg.UpdateNewMessage).Message != msg {
		t.Fatal("the answer itself is not changed")
	}
	short := &tg.UpdateShortSentMessage{ID: 3, Pts: 8, PtsCount: 1}
	if blankMessages(short) != short {
		t.Fatal("a short answer passes as is")
	}
}
