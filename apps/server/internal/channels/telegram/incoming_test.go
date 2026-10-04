package telegram

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gotd/td/tg"
)

func TestParseBotUpdate(t *testing.T) {
	from := `"from":{"id":70001,"is_bot":false,"first_name":"Пётр","last_name":"Соколов","username":"petr_s"},"chat":{"id":70001,"type":"private"}`
	cases := []struct {
		name        string
		json        string
		skip        bool
		text        string
		file        *File
		unsupported string
	}{
		{name: "text", json: `{"update_id":1,"message":{"message_id":7,` + from + `,"text":"Где заказ?"}}`, text: "Где заказ?"},
		{name: "photo takes the largest size and the caption",
			json: `{"update_id":1,"message":{"message_id":7,` + from + `,"caption":"чек","photo":[{"file_id":"s","file_size":10},{"file_id":"b","file_size":900}]}}`,
			text: "чек", file: &File{Name: "photo.jpg", MIME: "image/jpeg", Size: 900, botFileID: "b"}},
		{name: "document keeps its name and type",
			json: `{"update_id":1,"message":{"message_id":7,` + from + `,"document":{"file_id":"d","file_name":"смета.xlsx","mime_type":"application/vnd.ms-excel","file_size":2048}}}`,
			file: &File{Name: "смета.xlsx", MIME: "application/vnd.ms-excel", Size: 2048, botFileID: "d"}},
		{name: "voice gets a name",
			json: `{"update_id":1,"message":{"message_id":7,` + from + `,"voice":{"file_id":"v","file_size":5000}}}`,
			file: &File{Name: "voice.ogg", MIME: "audio/ogg", Size: 5000, botFileID: "v"}},
		{name: "animation is one file, not two",
			json: `{"update_id":1,"message":{"message_id":7,` + from + `,"animation":{"file_id":"a","file_name":"cat.mp4","mime_type":"video/mp4"},"document":{"file_id":"a","file_name":"cat.mp4","mime_type":"video/mp4"}}}`,
			file: &File{Name: "cat.mp4", MIME: "video/mp4", botFileID: "a"}},
		{name: "sticker", json: `{"update_id":1,"message":{"message_id":7,` + from + `,"sticker":{"file_id":"x"}}}`, unsupported: UnsupportedSticker},
		{name: "location", json: `{"update_id":1,"message":{"message_id":7,` + from + `,"location":{"latitude":55.7,"longitude":37.6}}}`, unsupported: UnsupportedLocation},
		{name: "contact", json: `{"update_id":1,"message":{"message_id":7,` + from + `,"contact":{"phone_number":"+7900","first_name":"A"}}}`, unsupported: UnsupportedContact},
		{name: "dice", json: `{"update_id":1,"message":{"message_id":7,` + from + `,"dice":{"emoji":"🎲","value":3}}}`, unsupported: UnsupportedOther},
		{name: "group", skip: true,
			json: `{"update_id":1,"message":{"message_id":7,"from":{"id":70001,"is_bot":false,"first_name":"П"},"chat":{"id":-100,"type":"supergroup"},"text":"всем"}}`},
		{name: "another bot", skip: true,
			json: `{"update_id":1,"message":{"message_id":7,"from":{"id":5,"is_bot":true,"first_name":"B"},"chat":{"id":5,"type":"private"},"text":"spam"}}`},
		{name: "edit", skip: true, json: `{"update_id":1,"edited_message":{"message_id":7,` + from + `,"text":"исправлено"}}`},
		{name: "callback", skip: true, json: `{"update_id":1,"callback_query":{"id":"1"}}`},
		{name: "service message", skip: true, json: `{"update_id":1,"message":{"message_id":7,` + from + `,"pinned_message":{"message_id":1}}}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			u, err := ParseBotUpdate([]byte(c.json))
			if err != nil || u.ID != 1 {
				t.Fatalf("parse: %+v %v", u, err)
			}
			if c.skip {
				if u.Message != nil {
					t.Fatalf("not skipped: %+v", u.Message)
				}
				return
			}
			m := u.Message
			if m == nil || m.ID != 7 || m.ChatID != 70001 || m.From.Name() != "Пётр Соколов" || m.From.Username != "petr_s" {
				t.Fatalf("message %+v", m)
			}
			if m.Text != c.text || m.Unsupported != c.unsupported {
				t.Fatalf("text %q unsupported %q", m.Text, m.Unsupported)
			}
			if c.file == nil && len(m.Files) != 0 || c.file != nil && (len(m.Files) != 1 || m.Files[0] != *c.file) {
				t.Fatalf("files %+v", m.Files)
			}
		})
	}
	if _, err := ParseBotUpdate([]byte("{")); err == nil {
		t.Fatal("broken JSON parsed")
	}
}

func TestBotAPI_GetUpdatesAndDownload(t *testing.T) {
	var offset any
	content := []byte("jpeg bytes")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bot" + token + "/getUpdates":
			raw, _ := io.ReadAll(r.Body)
			offset = string(raw)
			_, _ = io.WriteString(w, `{"ok":true,"result":[
				{"update_id":5,"message":{"message_id":1,"from":{"id":7,"first_name":"A"},"chat":{"id":7,"type":"private"},"text":"hi"}},
				{"update_id":6,"message":"not an object"}]}`)
		case "/bot" + token + "/getFile":
			raw, _ := io.ReadAll(r.Body)
			switch {
			case bytes.Contains(raw, []byte(`"big"`)):
				_, _ = io.WriteString(w, `{"ok":false,"error_code":400,"description":"Bad Request: file is too big"}`)
			default:
				_, _ = io.WriteString(w, `{"ok":true,"result":{"file_id":"b","file_size":10,"file_path":"photos/file_1.jpg"}}`)
			}
		case "/file/bot" + token + "/photos/file_1.jpg":
			_, _ = w.Write(content)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	api := NewBotAPI(srv.URL, nil)
	ctx := context.Background()

	ups, err := api.GetUpdates(ctx, token, 5, 10)
	if err != nil || len(ups) != 2 || ups[0].Message == nil || ups[0].Message.Text != "hi" || ups[1].ID != 6 || ups[1].Message != nil {
		t.Fatalf("updates %+v %v", ups, err)
	}
	if s, _ := offset.(string); !strings.Contains(s, `"offset":5`) || !strings.Contains(s, `"allowed_updates":["message"]`) {
		t.Fatalf("getUpdates body %v", offset)
	}

	var buf bytes.Buffer
	if err := api.Download(ctx, token, File{botFileID: "b"}, &buf, 100); err != nil || !bytes.Equal(buf.Bytes(), content) {
		t.Fatalf("download %q %v", buf.Bytes(), err)
	}
	if err := api.Download(ctx, token, File{botFileID: "b"}, io.Discard, 5); !errors.Is(err, ErrFileTooLarge) {
		t.Fatalf("over the limit: %v", err)
	}
	if err := api.Download(ctx, token, File{botFileID: "b", Size: 1000}, io.Discard, 100); !errors.Is(err, ErrFileTooLarge) {
		t.Fatalf("known size over the limit: %v", err)
	}
	err = api.Download(ctx, token, File{botFileID: "big"}, io.Discard, 100)
	if !errors.Is(err, ErrFileTooLarge) {
		t.Fatalf("too big for the Bot API: %v", err)
	}

	// The token is part of every URL and must not reach errors.
	srv.Close()
	err = api.Download(ctx, token, File{botFileID: "b"}, io.Discard, 100)
	if err == nil || strings.Contains(err.Error(), token) || strings.Contains(err.Error(), "AAH") {
		t.Fatalf("error leaks the token or is nil: %v", err)
	}
}

func TestListener_KeepsPrivateChatsWithPeople(t *testing.T) {
	l := &listener{selfID: 1, people: map[int64]Person{}, bots: map[int64]bool{}, hashes: map[int64]int64{}, saved: map[int64]int64{}}
	l.remember(map[int64]*tg.User{
		70001: {ID: 70001, FirstName: "Ольга", LastName: "К", Username: "olga_k"},
		90000: {ID: 90000, FirstName: "Shop", Bot: true},
	})
	photo := &tg.MessageMediaPhoto{Photo: &tg.Photo{ID: 3, AccessHash: 4, FileReference: []byte{5}, Sizes: []tg.PhotoSizeClass{
		&tg.PhotoStrippedSize{Type: "i"},
		&tg.PhotoSize{Type: "m", Size: 300},
		&tg.PhotoSizeProgressive{Type: "y", Sizes: []int{100, 900, 4000}},
	}}}
	sticker := &tg.MessageMediaDocument{Document: &tg.Document{ID: 6, Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeSticker{}}}}
	doc := &tg.MessageMediaDocument{Document: &tg.Document{ID: 7, AccessHash: 8, Size: 2048, MimeType: "application/pdf",
		Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeFilename{FileName: "договор.pdf"}}}}
	voice := &tg.MessageMediaDocument{Document: &tg.Document{ID: 9, MimeType: "audio/ogg",
		Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeAudio{Voice: true}}}}

	user := func(id int64) tg.PeerClass { return &tg.PeerUser{UserID: id} }
	cases := []struct {
		name string
		msg  *tg.Message
		keep bool
		want func(Message) bool
	}{
		{"person", &tg.Message{ID: 10, PeerID: user(70001), Message: "Добрый день"}, true,
			func(m Message) bool {
				return m.Text == "Добрый день" && m.From.Name() == "Ольга К" && m.ChatID == 70001 && !m.Out
			}},
		{"owner's reply", &tg.Message{ID: 11, PeerID: user(70001), Out: true, Message: "Здравствуйте"}, true,
			func(m Message) bool { return m.Out && m.From.ID == 70001 }},
		{"unknown person", &tg.Message{ID: 12, PeerID: user(70002), Message: "hi"}, true,
			func(m Message) bool { return m.From == Person{ID: 70002} }},
		{"photo", &tg.Message{ID: 13, PeerID: user(70001), Media: photo}, true,
			func(m Message) bool {
				f := m.Files[0]
				loc, ok := f.location.(*tg.InputPhotoFileLocation)
				return len(m.Files) == 1 && f.Size == 4000 && f.MIME == "image/jpeg" && ok && loc.ThumbSize == "y" && loc.ID == 3
			}},
		{"document", &tg.Message{ID: 14, PeerID: user(70001), Media: doc, Message: "подпишите"}, true,
			func(m Message) bool {
				return m.Text == "подпишите" && m.Files[0].Name == "договор.pdf" && m.Files[0].Size == 2048 && m.Files[0].MIME == "application/pdf"
			}},
		{"voice", &tg.Message{ID: 15, PeerID: user(70001), Media: voice}, true,
			func(m Message) bool { return m.Files[0].Name == "voice.ogg" }},
		{"sticker", &tg.Message{ID: 16, PeerID: user(70001), Media: sticker}, true,
			func(m Message) bool { return m.Unsupported == UnsupportedSticker && len(m.Files) == 0 }},
		{"location", &tg.Message{ID: 17, PeerID: user(70001), Media: &tg.MessageMediaGeo{}}, true,
			func(m Message) bool { return m.Unsupported == UnsupportedLocation }},
		{"link preview is text", &tg.Message{ID: 18, PeerID: user(70001), Message: "https://example.com", Media: &tg.MessageMediaWebPage{}}, true,
			func(m Message) bool { return m.Unsupported == "" && len(m.Files) == 0 }},
		{"group", &tg.Message{ID: 20, PeerID: &tg.PeerChat{ChatID: 5}, Message: "всем"}, false, nil},
		{"channel", &tg.Message{ID: 21, PeerID: &tg.PeerChannel{ChannelID: 5}, Message: "пост"}, false, nil},
		{"saved messages", &tg.Message{ID: 22, PeerID: user(1), Message: "заметка"}, false, nil},
		{"login codes", &tg.Message{ID: 23, PeerID: user(ServiceUserID), Message: "Код входа: 12345"}, false, nil},
		{"bot", &tg.Message{ID: 24, PeerID: user(90000), Message: "Ваш заказ"}, false, nil},
		{"empty", &tg.Message{ID: 25, PeerID: user(70001)}, false, nil},
	}
	for _, c := range cases {
		m, ok := l.convert(c.msg)
		if ok != c.keep {
			t.Fatalf("%s: kept=%v", c.name, ok)
		}
		if ok && (m.ID != int64(c.msg.ID) || !c.want(m)) {
			t.Fatalf("%s: %+v", c.name, m)
		}
	}

	// Service messages never reach the handler.
	handled := 0
	l.handle = func(context.Context, Message, Fetch) error { handled++; return nil }
	if err := l.onNewMessage(context.Background(), tg.Entities{}, &tg.UpdateNewMessage{Message: &tg.MessageService{ID: 30, PeerID: user(70001)}}); err != nil || handled != 0 {
		t.Fatalf("service message: %d %v", handled, err)
	}
	if err := l.onNewMessage(context.Background(), tg.Entities{}, &tg.UpdateNewMessage{Message: &tg.Message{ID: 31, PeerID: user(70001), Message: "ok"}}); err != nil || handled != 1 {
		t.Fatalf("message: %d %v", handled, err)
	}
}

type memState struct {
	st    *UpdateState
	saves int
}

func (m *memState) LoadState(context.Context) (UpdateState, bool, error) {
	if m.st == nil {
		return UpdateState{}, false, nil
	}
	return *m.st, true, nil
}

func (m *memState) SaveState(_ context.Context, st UpdateState) error {
	m.st = &st
	m.saves++
	return nil
}

func TestStateAdapter(t *testing.T) {
	ctx := context.Background()
	store := &memState{}
	a := &stateAdapter{store: store}
	if _, found, err := a.GetState(ctx, 1); found || err != nil {
		t.Fatalf("empty store: %v %v", found, err)
	}
	if err := a.SetPts(ctx, 1, 5); err == nil {
		t.Fatal("SetPts before any state must fail (gotd contract)")
	}
	if err := a.SetState(ctx, 1, struct{ Pts, Qts, Date, Seq int }{10, 1, 100, 2}); err != nil {
		t.Fatal(err)
	}
	_ = a.SetPts(ctx, 1, 11)
	_ = a.SetDateSeq(ctx, 1, 101, 3)
	_ = a.SetQts(ctx, 1, 4)
	if *store.st != (UpdateState{Pts: 11, Qts: 4, Date: 101, Seq: 3}) || store.saves != 4 {
		t.Fatalf("stored %+v after %d saves", store.st, store.saves)
	}
	// Channel positions stay in memory.
	_ = a.SetChannelPts(ctx, 1, 77, 9)
	if pts, ok, _ := a.GetChannelPts(ctx, 1, 77); !ok || pts != 9 || store.saves != 4 {
		t.Fatalf("channel pts %d %v", pts, ok)
	}

	// A fresh connection resumes from the store.
	b := &stateAdapter{store: store}
	if st, found, err := b.GetState(ctx, 1); !found || err != nil || st.Pts != 11 {
		t.Fatalf("resume: %+v %v %v", st, found, err)
	}
}

func TestPersistentSession(t *testing.T) {
	ctx := context.Background()
	var saved []byte
	s := &persistentSession{save: func(_ context.Context, b []byte) error { saved = b; return nil }}
	if _, err := s.LoadSession(ctx); err == nil {
		t.Fatal("empty session loaded")
	}
	if err := s.StoreSession(ctx, []byte("key")); err != nil || string(saved) != "key" {
		t.Fatalf("store: %q %v", saved, err)
	}
	if b, err := s.LoadSession(ctx); err != nil || string(b) != "key" {
		t.Fatalf("load: %q %v", b, err)
	}
	s.save = func(context.Context, []byte) error { return errors.New("db down") }
	if err := s.StoreSession(ctx, []byte("key2")); err == nil {
		t.Fatal("save error swallowed")
	}
}
