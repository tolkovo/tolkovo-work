package telegram

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	gotd "github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/message/unpack"
	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

// MaxTextLen is the longest text of one Telegram message, in characters.
const MaxTextLen = 4096

// MaxPhotoSize is the largest image sent as a photo; larger ones go as
// documents (sendPhoto takes up to 10 MB).
const MaxPhotoSize = 10 << 20

// Reasons Telegram refuses a message for good (docs/02 §11.4).
const (
	ReasonBlocked      = "blocked"
	ReasonChatNotFound = "chat_not_found"
	ReasonLimited      = "limited"
	ReasonRejected     = "rejected"
)

// UndeliverableError means Telegram refused the message and a retry would
// get the same answer. Detail is Telegram's description, without personal
// data or secrets.
type UndeliverableError struct {
	Reason string
	Detail string
}

func (e *UndeliverableError) Error() string {
	return fmt.Sprintf("telegram: message not delivered (%s): %s", e.Reason, e.Detail)
}

// ErrDuplicate means Telegram has this message already: an earlier attempt
// delivered it, its id is unknown.
var ErrDuplicate = errors.New("telegram: the message was sent before")

// OutFile describes a file to send; its bytes come from a reader.
type OutFile struct {
	Name string
	MIME string
	Size int64
	// AsPhoto sends an image as a photo (compressed, shown inline) rather
	// than a document.
	AsPhoto bool
}

// SplitText cuts text into pieces of at most MaxTextLen characters, at a
// line break or a space when there is one in the second half of a piece.
func SplitText(s string) []string {
	r := []rune(s)
	var out []string
	for len(r) > MaxTextLen {
		cut, space := MaxTextLen, 0
		for i := MaxTextLen; i > MaxTextLen/2; i-- {
			if r[i-1] == '\n' {
				cut, space = i, 0
				break
			}
			if space == 0 && unicode.IsSpace(r[i-1]) {
				space = i
			}
		}
		if space > 0 {
			cut = space
		}
		piece := strings.TrimRightFunc(string(r[:cut]), unicode.IsSpace)
		if piece != "" {
			out = append(out, piece)
		}
		r = []rune(strings.TrimLeftFunc(string(r[cut:]), unicode.IsSpace))
	}
	if len(r) > 0 {
		out = append(out, string(r))
	}
	return out
}

// SendMessage sends text to a private chat and returns the message id.
func (b *BotAPI) SendMessage(ctx context.Context, token string, chatID int64, text string) (int64, error) {
	var res struct {
		MessageID int64 `json:"message_id"`
	}
	err := b.call(ctx, token, "sendMessage", map[string]any{"chat_id": chatID, "text": text}, &res)
	return res.MessageID, botSendErr(err)
}

// SendNotice sends a service message to a private chat without link
// previews: a link in it, such as one from a customer's words, is not
// fetched for a preview.
func (b *BotAPI) SendNotice(ctx context.Context, token string, chatID int64, text string) (int64, error) {
	var res struct {
		MessageID int64 `json:"message_id"`
	}
	err := b.call(ctx, token, "sendMessage", map[string]any{
		"chat_id": chatID, "text": text, "link_preview_options": map[string]any{"is_disabled": true},
	}, &res)
	return res.MessageID, botSendErr(err)
}

// Button is an inline button under a message; Data (up to 64 bytes) comes
// back in the Callback when it is pressed.
type Button struct {
	Text string
	Data string
}

// SendButtons sends text with inline buttons, one row per slice, to a
// private chat and returns the message id.
func (b *BotAPI) SendButtons(ctx context.Context, token string, chatID int64, text string, rows [][]Button) (int64, error) {
	keyboard := make([][]map[string]string, 0, len(rows))
	for _, row := range rows {
		r := make([]map[string]string, 0, len(row))
		for _, btn := range row {
			r = append(r, map[string]string{"text": btn.Text, "callback_data": btn.Data})
		}
		keyboard = append(keyboard, r)
	}
	var res struct {
		MessageID int64 `json:"message_id"`
	}
	err := b.call(ctx, token, "sendMessage", map[string]any{
		"chat_id": chatID, "text": text, "reply_markup": map[string]any{"inline_keyboard": keyboard},
	}, &res)
	return res.MessageID, botSendErr(err)
}

// AnswerCallback ends the spinner of a pressed button; text, when not
// empty, shows briefly at the top of the chat.
func (b *BotAPI) AnswerCallback(ctx context.Context, token, callbackID, text string) error {
	params := map[string]any{"callback_query_id": callbackID}
	if text != "" {
		params["text"] = text
	}
	return b.call(ctx, token, "answerCallbackQuery", params, nil)
}

// EditText replaces the text of a message the bot sent and drops its
// buttons.
func (b *BotAPI) EditText(ctx context.Context, token string, chatID, messageID int64, text string) error {
	return b.call(ctx, token, "editMessageText", map[string]any{
		"chat_id": chatID, "message_id": messageID, "text": text,
	}, nil)
}

// SendFile uploads a file from r to a private chat (multipart, as the Bot
// API takes uploads) and returns the message id.
func (b *BotAPI) SendFile(ctx context.Context, token string, chatID int64, f OutFile, r io.Reader) (int64, error) {
	method, field := "sendDocument", "document"
	if f.AsPhoto {
		method, field = "sendPhoto", "photo"
	}
	if !ValidBotToken(token) {
		return 0, fmt.Errorf("telegram %s: %w", method, ErrBotToken)
	}
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		err := mw.WriteField("chat_id", strconv.FormatInt(chatID, 10))
		if err == nil {
			var part io.Writer
			if part, err = mw.CreateFormFile(field, safeFileName(f.Name)); err == nil {
				if _, err = io.Copy(part, r); err == nil {
					err = mw.Close()
				}
			}
		}
		pw.CloseWithError(err)
	}()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.base+"/bot"+token+"/"+method, pr)
	if err != nil {
		_ = pr.CloseWithError(err)
		return 0, fmt.Errorf("telegram %s: build request", method)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := b.files.Do(req)
	_ = pr.CloseWithError(errors.New("request finished"))
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return 0, fmt.Errorf("telegram %s: %w", method, err)
	}
	defer func() { _ = resp.Body.Close() }()
	var res struct {
		MessageID int64 `json:"message_id"`
	}
	err = decodeAnswer(method, resp, &res)
	return res.MessageID, botSendErr(err)
}

// SendChatAction shows "typing…" in the chat for about five seconds.
func (b *BotAPI) SendChatAction(ctx context.Context, token string, chatID int64) error {
	return b.call(ctx, token, "sendChatAction", map[string]any{"chat_id": chatID, "action": "typing"}, nil)
}

// safeFileName keeps a file name usable in a multipart header.
func safeFileName(name string) string {
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == '"' || r == '\\' {
			return '_'
		}
		return r
	}, name)
	if name == "" {
		return "file"
	}
	return name
}

// botSendErr turns refusals of a send into UndeliverableError; 429, server
// errors and network failures stay retryable.
func botSendErr(err error) error {
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.RetryAfter > 0 || apiErr.Code >= 500 {
		return err
	}
	d := strings.ToLower(apiErr.Description)
	switch {
	case apiErr.Code == http.StatusForbidden && strings.Contains(d, "blocked"):
		return &UndeliverableError{Reason: ReasonBlocked, Detail: apiErr.Description}
	case apiErr.Code == http.StatusForbidden, strings.Contains(d, "chat not found"), strings.Contains(d, "user not found"):
		// "user is deactivated", "bot can't initiate conversation with a user"
		return &UndeliverableError{Reason: ReasonChatNotFound, Detail: apiErr.Description}
	case apiErr.Code == http.StatusBadRequest, apiErr.Code == http.StatusRequestEntityTooLarge:
		return &UndeliverableError{Reason: ReasonRejected, Detail: apiErr.Description}
	}
	return err
}

// Peer is a person a personal account writes to: the user id and the access
// hash this account got for them.
type Peer struct {
	UserID     int64
	AccessHash int64
}

func (p Peer) input() *tg.InputPeerUser {
	return &tg.InputPeerUser{UserID: p.UserID, AccessHash: p.AccessHash}
}

// Conn is a connected personal account that can write. randomID makes a
// send idempotent: Telegram answers ErrDuplicate to a repeat.
type Conn interface {
	SendText(ctx context.Context, to Peer, randomID int64, text string) (int64, error)
	SendFile(ctx context.Context, to Peer, randomID int64, f OutFile, r io.Reader) (int64, error)
	// Typing shows "typing…" to the person for about five seconds.
	Typing(ctx context.Context, to Peer) error
	// ReadHistory marks the person's messages up to maxID as read.
	ReadHistory(ctx context.Context, to Peer, maxID int64) error
}

// conn implements Conn over a gotd client. feed passes the answers of the
// listening connection to its update manager, so a sent message keeps the
// update sequence whole and does not come back as news; nil for a
// connection that only sends.
type conn struct {
	api      *tg.Client
	feed     func(ctx context.Context, u tg.UpdatesClass)
	affected func(ctx context.Context, pts, count int)
}

func (c *conn) SendText(ctx context.Context, to Peer, randomID int64, text string) (int64, error) {
	res, err := c.api.MessagesSendMessage(ctx, &tg.MessagesSendMessageRequest{
		Peer: to.input(), Message: text, RandomID: randomID,
	})
	return c.sent(ctx, res, err)
}

func (c *conn) SendFile(ctx context.Context, to Peer, randomID int64, f OutFile, r io.Reader) (int64, error) {
	file, err := uploader.NewUploader(c.api).Upload(ctx, uploader.NewUpload(f.Name, r, f.Size))
	if err != nil {
		return 0, userSendErr(err)
	}
	var media tg.InputMediaClass = &tg.InputMediaUploadedDocument{
		File: file, MimeType: f.MIME,
		Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeFilename{FileName: f.Name}},
	}
	if f.AsPhoto {
		media = &tg.InputMediaUploadedPhoto{File: file}
	}
	res, err := c.api.MessagesSendMedia(ctx, &tg.MessagesSendMediaRequest{Peer: to.input(), Media: media, RandomID: randomID})
	return c.sent(ctx, res, err)
}

func (c *conn) Typing(ctx context.Context, to Peer) error {
	_, err := c.api.MessagesSetTyping(ctx, &tg.MessagesSetTypingRequest{Peer: to.input(), Action: &tg.SendMessageTypingAction{}})
	return userSendErr(err)
}

func (c *conn) ReadHistory(ctx context.Context, to Peer, maxID int64) error {
	res, err := c.api.MessagesReadHistory(ctx, &tg.MessagesReadHistoryRequest{Peer: to.input(), MaxID: int(maxID)})
	if err != nil {
		return userSendErr(err)
	}
	if c.affected != nil {
		c.affected(ctx, res.Pts, res.PtsCount)
	}
	return nil
}

// sent reads the message id from the answer and feeds the answer, with the
// message itself blanked out, to the update manager.
func (c *conn) sent(ctx context.Context, res tg.UpdatesClass, err error) (int64, error) {
	id, err := unpack.MessageID(res, err)
	if err != nil {
		return 0, userSendErr(err)
	}
	if c.feed != nil {
		c.feed(ctx, blankMessages(res))
	}
	return int64(id), nil
}

// blankMessages replaces new messages in u by empty ones with the same id:
// the update manager still counts their pts, the handler skips them.
func blankMessages(u tg.UpdatesClass) tg.UpdatesClass {
	v, ok := u.(*tg.Updates)
	if !ok {
		return u // updateShortSentMessage carries no message anyway
	}
	out := *v
	out.Updates = make([]tg.UpdateClass, len(v.Updates))
	for i, up := range v.Updates {
		if nm, ok := up.(*tg.UpdateNewMessage); ok {
			blank := *nm
			blank.Message = &tg.MessageEmpty{ID: nm.Message.GetID()}
			up = &blank
		}
		out.Updates[i] = up
	}
	return &out
}

// userSendErr turns MTProto refusals of a send into this package's errors.
func userSendErr(err error) error {
	if err == nil {
		return nil
	}
	if d, ok := tgerr.AsFloodWait(err); ok {
		return &FloodWaitError{Wait: d}
	}
	rpc, ok := tgerr.As(err)
	if !ok {
		return classify(err)
	}
	switch {
	case rpc.IsOneOf("RANDOM_ID_DUPLICATE"):
		return ErrDuplicate
	case rpc.IsOneOf("USER_IS_BLOCKED", "YOU_BLOCKED_USER"):
		return &UndeliverableError{Reason: ReasonBlocked, Detail: rpc.Type}
	case rpc.IsOneOf("PEER_ID_INVALID", "INPUT_USER_DEACTIVATED", "USER_ID_INVALID", "USER_PRIVACY_RESTRICTED"):
		return &UndeliverableError{Reason: ReasonChatNotFound, Detail: rpc.Type}
	case rpc.IsOneOf("PEER_FLOOD", "PRIVACY_PREMIUM_REQUIRED"):
		return &UndeliverableError{Reason: ReasonLimited, Detail: rpc.Type}
	}
	if c := classify(err); errors.Is(c, ErrSessionRevoked) || errors.Is(c, ErrBanned) {
		return c
	}
	if rpc.Code == http.StatusBadRequest || rpc.Code == http.StatusForbidden {
		return &UndeliverableError{Reason: ReasonRejected, Detail: rpc.Type}
	}
	return err
}

// Connect opens a connection that only sends, with the session of a
// personal account, and runs fn on it. The listening connection of the
// account may live in another process at the same time.
func (u *UserAuth) Connect(ctx context.Context, sess []byte, fn func(ctx context.Context, c Conn) error) error {
	_, err := u.runFor(ctx, 2*time.Minute, sess, func(ctx context.Context, client *gotd.Client) error {
		return fn(ctx, &conn{api: client.API()})
	})
	return err
}
