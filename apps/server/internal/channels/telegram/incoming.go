package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/gotd/td/tg"
)

// What a message holds when it has neither text nor files the panel can
// show (the same names as conversations.Unsupported*).
const (
	UnsupportedSticker  = "sticker"
	UnsupportedLocation = "location"
	UnsupportedContact  = "contact"
	UnsupportedPoll     = "poll"
	UnsupportedOther    = "other"
)

// ServiceUserID is Telegram's service account: login codes and security
// notices come from it, and they are never read (ADR-0014).
const ServiceUserID = 777000

// ErrFileTooLarge means Telegram will not give the file (Bot API downloads
// stop at 20 MB) or it exceeds the limit of the caller.
var ErrFileTooLarge = errors.New("telegram: file is too large")

// Message is a private message from a person, from the Bot API or MTProto.
// Group chats, channels, bots and service messages never become one.
type Message struct {
	// ID is the message id; unique within the chat.
	ID int64
	// ChatID is the chat with the person: their user id.
	ChatID int64
	// From is the person (also for Out messages).
	From Person
	// Out: the owner of a personal account wrote it in their own app.
	Out         bool
	Text        string
	Files       []File
	Unsupported string
}

// Person is a Telegram user as an update describes them.
type Person struct {
	ID        int64
	FirstName string
	LastName  string
	Username  string
}

// Name is the person's display name.
func (p Person) Name() string { return strings.TrimSpace(p.FirstName + " " + p.LastName) }

// File is a file of a message. Download it through the connection that
// received the message: BotAPI.Download or the Fetch of Listen.
type File struct {
	Name string
	MIME string
	Size int64 // 0 when Telegram did not say
	// botFileID or location identify the file for the download.
	botFileID string
	location  tg.InputFileLocationClass
}

// BotUpdate is one update of getUpdates or a webhook. Message is nil when
// the update is nothing a support inbox stores; Callback is set when a
// person pressed an inline button of the bot in a private chat.
type BotUpdate struct {
	ID       int64
	Message  *Message
	Callback *Callback
}

// Callback is a press of an inline button: Data is what the button carried.
// Answer it with BotAPI.AnswerCallback, or the button keeps spinning.
type Callback struct {
	ID        string
	ChatID    int64
	MessageID int64 // the message with the buttons
	From      Person
	Data      string
}

type botUser struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Username  string `json:"username"`
}

type botFile struct {
	FileID   string `json:"file_id"`
	FileName string `json:"file_name"`
	MIMEType string `json:"mime_type"`
	FileSize int64  `json:"file_size"`
}

type botMessage struct {
	MessageID int64    `json:"message_id"`
	From      *botUser `json:"from"`
	Chat      struct {
		ID   int64  `json:"id"`
		Type string `json:"type"`
	} `json:"chat"`
	Text      string          `json:"text"`
	Caption   string          `json:"caption"`
	Photo     []botFile       `json:"photo"`
	Document  *botFile        `json:"document"`
	Video     *botFile        `json:"video"`
	Animation *botFile        `json:"animation"`
	Audio     *botFile        `json:"audio"`
	Voice     *botFile        `json:"voice"`
	VideoNote *botFile        `json:"video_note"`
	Sticker   json.RawMessage `json:"sticker"`
	Location  json.RawMessage `json:"location"`
	Venue     json.RawMessage `json:"venue"`
	Contact   json.RawMessage `json:"contact"`
	Poll      json.RawMessage `json:"poll"`
	Dice      json.RawMessage `json:"dice"`
	Game      json.RawMessage `json:"game"`
	Story     json.RawMessage `json:"story"`
	Invoice   json.RawMessage `json:"invoice"`
}

// ParseBotUpdate reads an update of the Bot API. Only new messages in
// private chats from people make a Message, and presses of the bot's
// buttons there a Callback; edits, membership changes, groups and bots are
// skipped.
func ParseBotUpdate(raw []byte) (BotUpdate, error) {
	var u struct {
		UpdateID int64       `json:"update_id"`
		Message  *botMessage `json:"message"`
		Callback *struct {
			ID      string      `json:"id"`
			From    *botUser    `json:"from"`
			Message *botMessage `json:"message"`
			Data    string      `json:"data"`
		} `json:"callback_query"`
	}
	if err := json.Unmarshal(raw, &u); err != nil {
		return BotUpdate{}, fmt.Errorf("telegram: decode update: %w", err)
	}
	out := BotUpdate{ID: u.UpdateID}
	if c := u.Callback; c != nil && c.From != nil && !c.From.IsBot && c.Message != nil && c.Message.Chat.Type == "private" {
		out.Callback = &Callback{
			ID: c.ID, ChatID: c.Message.Chat.ID, MessageID: c.Message.MessageID, Data: c.Data,
			From: Person{ID: c.From.ID, FirstName: c.From.FirstName, LastName: c.From.LastName, Username: c.From.Username},
		}
		return out, nil
	}
	m := u.Message
	if m == nil || m.Chat.Type != "private" || m.From == nil || m.From.IsBot || m.From.ID == ServiceUserID {
		return out, nil
	}
	msg := &Message{
		ID: m.MessageID, ChatID: m.Chat.ID,
		From: Person{ID: m.From.ID, FirstName: m.From.FirstName, LastName: m.From.LastName, Username: m.From.Username},
		Text: m.Text,
	}
	if msg.Text == "" {
		msg.Text = m.Caption
	}
	if n := len(m.Photo); n > 0 {
		// Sizes come smallest first.
		p := m.Photo[n-1]
		msg.Files = append(msg.Files, File{Name: "photo.jpg", MIME: "image/jpeg", Size: p.FileSize, botFileID: p.FileID})
	}
	for _, f := range []struct {
		file       *botFile
		name, mime string
	}{
		{m.Document, "file", "application/octet-stream"},
		{m.Video, "video.mp4", "video/mp4"},
		{m.Animation, "animation.mp4", "video/mp4"},
		{m.Audio, "audio.mp3", "audio/mpeg"},
		{m.Voice, "voice.ogg", "audio/ogg"},
		{m.VideoNote, "video.mp4", "video/mp4"},
	} {
		if f.file == nil || f.file.FileID == "" {
			continue
		}
		name, mime := f.file.FileName, f.file.MIMEType
		if name == "" {
			name = f.name
		}
		if mime == "" {
			mime = f.mime
		}
		msg.Files = append(msg.Files, File{Name: name, MIME: mime, Size: f.file.FileSize, botFileID: f.file.FileID})
		break // an animation also comes as a document: one file per message
	}
	switch {
	case len(m.Sticker) > 0:
		msg.Unsupported = UnsupportedSticker
	case len(m.Location) > 0 || len(m.Venue) > 0:
		msg.Unsupported = UnsupportedLocation
	case len(m.Contact) > 0:
		msg.Unsupported = UnsupportedContact
	case len(m.Poll) > 0:
		msg.Unsupported = UnsupportedPoll
	case len(m.Dice) > 0 || len(m.Game) > 0 || len(m.Story) > 0 || len(m.Invoice) > 0:
		msg.Unsupported = UnsupportedOther
	}
	if msg.Text == "" && len(msg.Files) == 0 && msg.Unsupported == "" {
		return out, nil // a service message: pinned, joined, payment…
	}
	if msg.Unsupported != "" && (msg.Text != "" || len(msg.Files) > 0) {
		msg.Unsupported = "" // a venue has a title, a story a caption: text is enough
	}
	out.Message = msg
	return out, nil
}

// GetUpdates long-polls the bot's updates after offset (the last seen
// update id + 1), waiting up to timeout seconds. Calling it with an offset
// confirms the updates before it: Telegram drops them.
func (b *BotAPI) GetUpdates(ctx context.Context, token string, offset int64, timeout int) ([]BotUpdate, error) {
	var raw []json.RawMessage
	if err := b.call(ctx, token, "getUpdates", map[string]any{
		"offset": offset, "timeout": timeout, "allowed_updates": []string{"message"},
	}, &raw); err != nil {
		return nil, err
	}
	out := make([]BotUpdate, 0, len(raw))
	for _, r := range raw {
		u, err := ParseBotUpdate(r)
		if err != nil {
			// The id is needed to move past the update.
			var id struct {
				UpdateID int64 `json:"update_id"`
			}
			if json.Unmarshal(r, &id) != nil {
				return nil, err
			}
			u = BotUpdate{ID: id.UpdateID}
		}
		out = append(out, u)
	}
	return out, nil
}

// Download writes a file of a bot message to w, at most limit bytes
// (getFile, then the file URL; both hold the token and are never printed).
func (b *BotAPI) Download(ctx context.Context, token string, f File, w io.Writer, limit int64) error {
	if f.botFileID == "" {
		return errors.New("telegram: not a bot file")
	}
	if f.Size > limit {
		return ErrFileTooLarge
	}
	var info struct {
		FilePath string `json:"file_path"`
		FileSize int64  `json:"file_size"`
	}
	if err := b.call(ctx, token, "getFile", map[string]any{"file_id": f.botFileID}, &info); err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && strings.Contains(strings.ToLower(apiErr.Description), "too big") {
			return ErrFileTooLarge
		}
		return err
	}
	if info.FileSize > limit {
		return ErrFileTooLarge
	}
	if info.FilePath == "" {
		return errors.New("telegram getFile: no file path")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.base+"/file/bot"+token+"/"+info.FilePath, nil)
	if err != nil {
		return errors.New("telegram file: build request")
	}
	resp, err := b.files.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return fmt.Errorf("telegram file: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("telegram file: HTTP %d", resp.StatusCode)
	}
	return copyLimited(w, resp.Body, limit)
}

// copyLimited copies r to w and fails with ErrFileTooLarge past limit.
func copyLimited(w io.Writer, r io.Reader, limit int64) error {
	n, err := io.Copy(w, io.LimitReader(r, limit+1))
	if err != nil {
		return err
	}
	if n > limit {
		return ErrFileTooLarge
	}
	return nil
}

// limitWriter fails with ErrFileTooLarge once more than n bytes come.
type limitWriter struct {
	w io.Writer
	n int64
}

func (l *limitWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > l.n {
		return 0, ErrFileTooLarge
	}
	l.n -= int64(len(p))
	return l.w.Write(p)
}
