package channels

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/tolkovo/tolkovo/apps/server/internal/channels/store"
	"github.com/tolkovo/tolkovo/apps/server/internal/channels/telegram"
	"github.com/tolkovo/tolkovo/apps/server/internal/conversations"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/jobs"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/ratelimit"
)

// Outbound limits (docs/02 §11.5): Telegram allows a bot about 30 messages a
// second and one a second into a chat; a personal account is kept well
// below what gets it flagged as spam.
type sendLimits struct {
	bot, user, chat ratelimit.Rule
}

var defaultLimits = sendLimits{
	bot:  ratelimit.Rule{Limit: 30, Window: time.Second},
	user: ratelimit.Rule{Limit: 30, Window: time.Minute},
	chat: ratelimit.Rule{Limit: 1, Window: time.Second},
}

// Retries of a send: 1 s doubling up to 10 minutes, 12 attempts (about 40
// minutes), then the reply is failed and the operator may retry it.
const (
	sendAttempts = 12
	maxBackoff   = 10 * time.Minute
	// waitTurn: how soon a reply checks again whether the earlier one of its
	// conversation went out.
	waitTurn = 2 * time.Second
	// sendingTTL bounds the mark of a send in flight; it outlives the job.
	sendingTTL = 3 * time.Minute
	// sentTTL keeps the ids of sent messages for recognising them when the
	// personal account's stream brings them back (after a restart too).
	sentTTL = 48 * time.Hour
	// typingEvery: Telegram shows typing for about five seconds.
	typingEvery = 4 * time.Second
)

// SendArgs delivers one operator reply to its channel. Only ids: the text
// stays in messages.
type SendArgs struct {
	WorkspaceID uuid.UUID `json:"workspace_id"`
	MessageID   uuid.UUID `json:"message_id"`
}

// Kind implements river.JobArgs.
func (SendArgs) Kind() string { return "channels.send" }

// InsertOpts implements river.JobArgsWithInsertOpts: one live job per
// reply; a retry by the operator queues a new one after the last ended.
func (SendArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{
		Queue: jobs.QueueOutbound, MaxAttempts: sendAttempts,
		UniqueOpts: river.UniqueOpts{
			ByArgs: true,
			ByState: []rivertype.JobState{
				rivertype.JobStateAvailable, rivertype.JobStatePending, rivertype.JobStateRunning,
				rivertype.JobStateRetryable, rivertype.JobStateScheduled,
			},
		},
	}
}

// SetJobs connects the job queue replies are inserted into (role api);
// call it before serving.
func (s *Service) SetJobs(c *jobs.Client) { s.opts.Jobs = c }

// QueueDelivery implements conversations.Deliverer: it queues the reply in
// the transaction that stores it. A reply to a widget visitor is also
// pushed to their widget in the same transaction (W-01): the widget is our
// own transport, so the push is the delivery, and the job only marks it sent.
func (s *Service) QueueDelivery(ctx context.Context, tx pgx.Tx, r conversations.QueuedReply) error {
	if s.opts.Jobs == nil {
		return errors.New("channels: no job queue to deliver replies")
	}
	if r.ChannelID != nil && r.Thread != "" {
		ch, err := store.New(tx).GetChannel(ctx, store.GetChannelParams{WorkspaceID: r.WorkspaceID, ID: *r.ChannelID})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err == nil && ch.Type == TypeWidget {
			if err := queueWidgetPush(ctx, tx, r); err != nil {
				return err
			}
		}
	}
	_, err := s.opts.Jobs.InsertTx(ctx, tx, SendArgs{WorkspaceID: r.WorkspaceID, MessageID: r.MessageID}, nil)
	return err
}

// AddWorkers registers the send job; the worker role calls it.
func (s *Service) AddWorkers(workers *river.Workers) {
	river.AddWorker(workers, &sendWorker{svc: s})
}

type sendWorker struct {
	river.WorkerDefaults[SendArgs]
	svc *Service
}

// Timeout covers a reply of several pieces with files.
func (w *sendWorker) Timeout(*river.Job[SendArgs]) time.Duration { return sendingTTL - 30*time.Second }

// NextRetry doubles the wait from a second up to maxBackoff.
func (w *sendWorker) NextRetry(job *river.Job[SendArgs]) time.Time {
	return time.Now().Add(backoff(job.Attempt))
}

func backoff(attempt int) time.Duration {
	if attempt < 1 || attempt > 10 {
		return maxBackoff
	}
	return min(time.Second<<(attempt-1), maxBackoff)
}

// Work implements river.Worker.
func (w *sendWorker) Work(ctx context.Context, job *river.Job[SendArgs]) error {
	return w.svc.send(ctx, job.Args, job.Attempt >= job.MaxAttempts)
}

// snoozeError asks to try again later without spending an attempt.
type snoozeError struct{ wait time.Duration }

func (e *snoozeError) Error() string { return fmt.Sprintf("channels: send again in %s", e.wait) }

// send delivers a pending reply piece by piece: its text (split at
// telegram.MaxTextLen), then each file. Pieces sent by earlier attempts are
// skipped. last is the final attempt: a retryable failure then fails the
// reply for good.
func (s *Service) send(ctx context.Context, a SendArgs, last bool) error {
	out, err := s.opts.Inbox.Outgoing(ctx, a.WorkspaceID, a.MessageID)
	if errors.Is(err, conversations.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if out.Status != conversations.DeliveryPending {
		return nil
	}
	if out.Waiting {
		return river.JobSnooze(waitTurn)
	}
	fail := func(reason string) error {
		return s.opts.Inbox.SetDelivery(ctx, a.WorkspaceID, a.MessageID, conversations.Delivery{
			Status: conversations.DeliveryFailed, Error: reason,
		})
	}
	if out.ChannelID == nil {
		return fail(conversations.DeliveryChannelUnavailable)
	}
	ch, raw, err := s.loadActive(ctx, a.WorkspaceID, *out.ChannelID)
	if errors.Is(err, ErrNotFound) {
		return fail(conversations.DeliveryChannelUnavailable)
	}
	if err != nil {
		return err
	}
	if ch.Type == TypeApp || ch.Type == TypeWidget {
		// The app fetches its replies itself (docs/02 §11.6, AP-06), the widget
		// got its push with the reply (W-01): a stored reply is out.
		return s.opts.Inbox.SetDelivery(ctx, a.WorkspaceID, a.MessageID, conversations.Delivery{Status: conversations.DeliverySent})
	}
	chatID, err := strconv.ParseInt(out.Thread, 10, 64)
	if err != nil {
		return fail(conversations.DeliveryChatNotFound)
	}
	ext, err := s.sendTo(ctx, a.WorkspaceID, ch, raw, chatID, out)

	var (
		undeliverable *telegram.UndeliverableError
		flood         *telegram.FloodWaitError
		apiErr        *telegram.APIError
		snooze        *snoozeError
	)
	switch {
	case err == nil:
		return s.opts.Inbox.SetDelivery(ctx, a.WorkspaceID, a.MessageID, conversations.Delivery{
			Status: conversations.DeliverySent, ExternalID: ext,
		})
	case errors.As(err, &snooze):
		return river.JobSnooze(snooze.wait)
	case errors.As(err, &flood):
		return river.JobSnooze(flood.Wait)
	case errors.As(err, &apiErr) && apiErr.RetryAfter > 0:
		return river.JobSnooze(apiErr.RetryAfter)
	case errors.As(err, &undeliverable):
		s.log.Info("telegram refused a reply", "workspace_id", a.WorkspaceID, "message_id", a.MessageID,
			"reason", undeliverable.Reason, "detail", undeliverable.Detail)
		return fail(undeliverable.Reason)
	case errors.Is(err, errNoPeer):
		return fail(conversations.DeliveryChatNotFound)
	case errors.Is(err, ErrUserUnavailable):
		return fail(conversations.DeliveryChannelUnavailable)
	}
	if code := channelErrorCode(err); code != "" {
		s.log.Warn("telegram channel needs the admin", "workspace_id", a.WorkspaceID, "channel_id", ch.ID, "error_code", code)
		s.setError(context.WithoutCancel(ctx), a.WorkspaceID, ch.ID, code)
		return fail(conversations.DeliveryChannelUnavailable)
	}
	if last {
		s.log.Warn("telegram reply failed after every retry", "workspace_id", a.WorkspaceID, "message_id", a.MessageID, "error", err)
		return fail(conversations.DeliveryGaveUp)
	}
	return err
}

// channelErrorCode names a failure that breaks the whole channel.
func channelErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrBotToken), errors.Is(err, telegram.ErrBotToken):
		return ErrorTokenRevoked
	case errors.Is(err, telegram.ErrSessionRevoked):
		return ErrorSessionRevoked
	case errors.Is(err, telegram.ErrBanned):
		return ErrorBanned
	}
	return ""
}

// errNoPeer: a personal account has not seen the person since T-03 started
// recording access hashes, so it cannot address them.
var errNoPeer = errors.New("channels: the account does not know this person")

// piece is one Telegram message of a reply: a text or a file.
type piece struct {
	text string
	file *conversations.OutgoingFile
}

func pieces(out conversations.Outgoing) []piece {
	var ps []piece
	if out.Body != "" {
		for _, t := range telegram.SplitText(out.Body) {
			ps = append(ps, piece{text: t})
		}
	}
	for i := range out.Files {
		ps = append(ps, piece{file: &out.Files[i]})
	}
	return ps
}

// pieceSender sends one piece and returns its message id.
type pieceSender func(ctx context.Context, i int, p piece) (int64, error)

// sendTo sends the pieces not yet sent through the channel's connection and
// returns the id of the last message.
func (s *Service) sendTo(
	ctx context.Context, workspaceID uuid.UUID, ch Channel, raw []byte, chatID int64, out conversations.Outgoing,
) (string, error) {
	switch ch.Type {
	case TypeTelegramBot:
		var sec botSecret
		if err := json.Unmarshal(raw, &sec); err != nil {
			return "", fmt.Errorf("decode bot secret: %w", err)
		}
		last := len(pieces(out)) - 1
		return s.sendPieces(ctx, workspaceID, ch, out, s.limits.bot, func(ctx context.Context, i int, p piece) (int64, error) {
			if p.file == nil && i == last && out.OfferPerson {
				return s.opts.Bots.SendButtons(ctx, sec.Token, chatID, p.text, personButton(out.Locale))
			}
			if p.file == nil {
				return s.opts.Bots.SendMessage(ctx, sec.Token, chatID, p.text)
			}
			return s.sendFile(ctx, *p.file, func(ctx context.Context, f telegram.OutFile, r io.Reader) (int64, error) {
				return s.opts.Bots.SendFile(ctx, sec.Token, chatID, f, r)
			})
		})
	case TypeTelegramUser:
		peer, err := s.peer(ctx, workspaceID, ch.ID, chatID)
		if err != nil {
			return "", err
		}
		send := func(c telegram.Conn) (string, error) {
			done := s.markSending(ctx, ch.ID, out.Thread)
			defer done()
			return s.sendPieces(ctx, workspaceID, ch, out, s.limits.user, func(ctx context.Context, i int, p piece) (int64, error) {
				rid := randomID(out.MessageID, i)
				var (
					id  int64
					err error
				)
				if p.file == nil {
					id, err = c.SendText(ctx, peer, rid, p.text)
				} else {
					id, err = s.sendFile(ctx, *p.file, func(ctx context.Context, f telegram.OutFile, r io.Reader) (int64, error) {
						return c.SendFile(ctx, peer, rid, f, r)
					})
				}
				if err == nil {
					s.rememberSent(ctx, ch.ID, out.Thread, id)
				}
				return id, err
			})
		}
		if c := s.liveConn(ch.ID); c != nil {
			return send(c)
		}
		if s.opts.Users == nil {
			return "", ErrUserUnavailable
		}
		var sec userSecret
		if err := json.Unmarshal(raw, &sec); err != nil {
			return "", fmt.Errorf("decode account secret: %w", err)
		}
		var ext string
		err = s.opts.Users.Connect(ctx, sec.Session, func(ctx context.Context, c telegram.Conn) error {
			var err error
			ext, err = send(c)
			return err
		})
		return ext, err
	}
	return "", fmt.Errorf("channels: cannot send through a %s channel", ch.Type)
}

// sendPieces walks the pieces of a reply under the rate limits and records
// each one sent, so a retry continues after it.
func (s *Service) sendPieces(
	ctx context.Context, workspaceID uuid.UUID, ch Channel, out conversations.Outgoing, rate ratelimit.Rule, send pieceSender,
) (string, error) {
	ext := lastKnown(out.SentParts)
	for i, p := range pieces(out) {
		if i < len(out.SentParts) {
			continue
		}
		if err := s.allow(ctx, ch.ID, out.Thread, rate); err != nil {
			return "", err
		}
		id, err := send(ctx, i, p)
		if errors.Is(err, telegram.ErrDuplicate) {
			id, err = 0, nil // an attempt that died after sending delivered it
		}
		if err != nil {
			return "", err
		}
		part := strconv.FormatInt(id, 10)
		if err := s.opts.Inbox.AddSentPart(ctx, workspaceID, out.MessageID, part); err != nil {
			return "", err
		}
		if id != 0 {
			ext = part
		}
	}
	return ext, nil
}

func lastKnown(parts []string) string {
	for i := len(parts) - 1; i >= 0; i-- {
		if parts[i] != "0" {
			return parts[i]
		}
	}
	return ""
}

// sendFile streams an attachment from storage. An image Telegram refuses as
// a photo (dimensions, format) goes again as a document.
func (s *Service) sendFile(
	ctx context.Context, a conversations.OutgoingFile, send func(ctx context.Context, f telegram.OutFile, r io.Reader) (int64, error),
) (int64, error) {
	f := telegram.OutFile{
		Name: a.FileName, MIME: a.ContentType, Size: a.Size,
		AsPhoto: (a.ContentType == "image/jpeg" || a.ContentType == "image/png") && a.Size <= telegram.MaxPhotoSize,
	}
	id, err := s.streamFile(ctx, a.StorageKey, f, send)
	var undeliverable *telegram.UndeliverableError
	if f.AsPhoto && errors.As(err, &undeliverable) && undeliverable.Reason == telegram.ReasonRejected {
		f.AsPhoto = false
		return s.streamFile(ctx, a.StorageKey, f, send)
	}
	return id, err
}

func (s *Service) streamFile(
	ctx context.Context, key string, f telegram.OutFile, send func(ctx context.Context, f telegram.OutFile, r io.Reader) (int64, error),
) (int64, error) {
	if s.opts.Files == nil {
		return 0, errors.New("channels: no file storage")
	}
	r, obj, err := s.opts.Files.Open(ctx, key)
	if err != nil {
		return 0, fmt.Errorf("open attachment: %w", err)
	}
	defer func() { _ = r.Close() }()
	f.Size = obj.Size
	return send(ctx, f, r)
}

// allow takes a token of the channel's and the chat's limits; a full bucket
// snoozes the job.
func (s *Service) allow(ctx context.Context, channelID uuid.UUID, thread string, rate ratelimit.Rule) error {
	if s.limiter == nil {
		return nil
	}
	base := "tg:out:" + channelID.String()
	for _, l := range []struct {
		key  string
		rule ratelimit.Rule
	}{{base, rate}, {base + ":" + thread, s.limits.chat}} {
		ok, wait, err := s.limiter.Allow(ctx, l.key, l.rule)
		if err != nil {
			return err
		}
		if !ok {
			return &snoozeError{wait: wait}
		}
	}
	return nil
}

// randomID is the MTProto random_id of a piece: the same for every attempt,
// so Telegram drops a repeat.
func randomID(messageID uuid.UUID, piece int) int64 {
	h := sha256.Sum256(append(messageID[:], byte(piece))) //nolint:gosec // G115: a reply has at most 13 pieces
	id := int64(binary.BigEndian.Uint64(h[:8]) >> 1)      //nolint:gosec // G115: shifted into the positive range
	if id == 0 {
		id = 1
	}
	return id
}

// peer returns the access hash of a person for a personal account.
func (s *Service) peer(ctx context.Context, workspaceID, channelID uuid.UUID, userID int64) (telegram.Peer, error) {
	var hash int64
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		var err error
		hash, err = store.New(tx).GetPeer(ctx, store.GetPeerParams{WorkspaceID: workspaceID, ChannelID: channelID, UserID: userID})
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return telegram.Peer{}, errNoPeer
	}
	if err != nil {
		return telegram.Peer{}, fmt.Errorf("load peer: %w", err)
	}
	return telegram.Peer{UserID: userID, AccessHash: hash}, nil
}

func (s *Service) savePeer(ctx context.Context, workspaceID, channelID uuid.UUID, p telegram.Peer) error {
	return s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		return store.New(tx).PutPeer(ctx, store.PutPeerParams{
			WorkspaceID: workspaceID, ChannelID: channelID, UserID: p.UserID, AccessHash: p.AccessHash,
		})
	})
}

// Echo protection for personal accounts. A message the account sends comes
// back in its own update stream as if the owner wrote it on the phone. The
// sender marks the chat while a send is in flight and remembers the ids it
// got; the listener waits out the mark and skips remembered ids.

func sendingKey(channelID uuid.UUID, thread string) string {
	return "tg:sending:" + channelID.String() + ":" + thread
}

func sentKey(channelID uuid.UUID, thread string, id int64) string {
	return "tg:sent:" + channelID.String() + ":" + thread + ":" + strconv.FormatInt(id, 10)
}

// markSending marks the chat until the returned func is called.
func (s *Service) markSending(ctx context.Context, channelID uuid.UUID, thread string) func() {
	if s.opts.Redis == nil {
		return func() {}
	}
	key := sendingKey(channelID, thread)
	_ = s.opts.Redis.Set(ctx, key, "1", sendingTTL).Err()
	return func() { _ = s.opts.Redis.Del(context.WithoutCancel(ctx), key).Err() }
}

func (s *Service) rememberSent(ctx context.Context, channelID uuid.UUID, thread string, id int64) {
	if s.opts.Redis == nil || id == 0 {
		return
	}
	_ = s.opts.Redis.Set(context.WithoutCancel(ctx), sentKey(channelID, thread, id), "1", sentTTL).Err()
}

// sentByUs reports whether an outgoing message of the account's stream is a
// reply Tolkovo sent. It waits while a send to the chat is in flight.
func (s *Service) sentByUs(ctx context.Context, channelID uuid.UUID, m telegram.Message) bool {
	if s.opts.Redis == nil {
		return false
	}
	thread := strconv.FormatInt(m.ChatID, 10)
	deadline := time.Now().Add(sendingTTL)
	for time.Now().Before(deadline) {
		n, err := s.opts.Redis.Exists(ctx, sendingKey(channelID, thread)).Result()
		if err != nil || n == 0 {
			break
		}
		if sleep(ctx, 200*time.Millisecond) != nil {
			return false
		}
	}
	n, err := s.opts.Redis.Exists(ctx, sentKey(channelID, thread, m.ID)).Result()
	return err == nil && n > 0
}

// liveConn is the listening connection of a personal account held by this
// process, nil when another worker holds it.
func (s *Service) liveConn(channelID uuid.UUID) telegram.Conn {
	s.liveMu.Lock()
	defer s.liveMu.Unlock()
	return s.live[channelID]
}

func (s *Service) setLive(channelID uuid.UUID, c telegram.Conn) {
	s.liveMu.Lock()
	defer s.liveMu.Unlock()
	if c == nil {
		delete(s.live, channelID)
		return
	}
	s.live[channelID] = c
}

// Actions an operator takes in the panel that a channel shows the customer:
// typing a reply and reading the chat (docs/02 §10.5). The api and realtime
// roles publish them to Redis; the worker that holds the connection acts.
const actionsChannel = "channels:actions"

const (
	actionTyping = "typing"
	actionRead   = "read"
)

type action struct {
	Kind           string    `json:"kind"`
	WorkspaceID    uuid.UUID `json:"workspace_id"`
	ConversationID uuid.UUID `json:"conversation_id"`
}

// ReplyTyping tells the customer an operator is typing a reply; best
// effort, it never fails the caller.
func (s *Service) ReplyTyping(ctx context.Context, workspaceID, conversationID uuid.UUID) {
	s.publish(ctx, action{Kind: actionTyping, WorkspaceID: workspaceID, ConversationID: conversationID})
}

// ConversationRead marks the chat read in Telegram after an operator read
// the conversation (personal accounts only); best effort.
func (s *Service) ConversationRead(ctx context.Context, workspaceID, conversationID uuid.UUID) {
	s.publish(ctx, action{Kind: actionRead, WorkspaceID: workspaceID, ConversationID: conversationID})
}

func (s *Service) publish(ctx context.Context, a action) {
	if s.opts.Redis == nil {
		return
	}
	b, _ := json.Marshal(a) // ids and a constant: cannot fail
	if err := s.opts.Redis.Publish(ctx, actionsChannel, b).Err(); err != nil && ctx.Err() == nil {
		s.log.Warn("publish channel action", "workspace_id", a.WorkspaceID, "error", err)
	}
}

// runActions carries out the published actions until ctx is done. Every
// worker hears every action: a bot's typing goes out from whichever wins
// the throttle key, a personal account's from the worker holding it.
func (s *Service) runActions(ctx context.Context) {
	if s.opts.Redis == nil {
		return
	}
	sub := s.opts.Redis.Subscribe(ctx, actionsChannel)
	defer func() { _ = sub.Close() }()
	busy := make(chan struct{}, 16)
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-sub.Channel():
			if !ok {
				return
			}
			var a action
			if json.Unmarshal([]byte(msg.Payload), &a) != nil {
				continue
			}
			select {
			case busy <- struct{}{}:
			default:
				continue // an action is a moment of an indicator: drop it when swamped
			}
			go func() {
				defer func() { <-busy }()
				actx, cancel := context.WithTimeout(ctx, 10*time.Second)
				defer cancel()
				if err := s.act(actx, a); err != nil && actx.Err() == nil {
					s.log.Debug("channel action failed", "workspace_id", a.WorkspaceID, "kind", a.Kind, "error", err)
				}
			}()
		}
	}
}

func (s *Service) act(ctx context.Context, a action) error {
	th, err := s.opts.Inbox.ConversationThread(ctx, a.WorkspaceID, a.ConversationID)
	if err != nil {
		return nil //nolint:nilerr // a conversation without a channel has nothing to show
	}
	ch, raw, err := s.loadActive(ctx, a.WorkspaceID, th.ChannelID)
	if err != nil {
		return err
	}
	if ch.Type == TypeWidget {
		if a.Kind != actionTyping {
			return nil // the widget reports reading itself (internal/widget)
		}
		return s.widgetTyping(ctx, a, th.Thread)
	}
	chatID, err := strconv.ParseInt(th.Thread, 10, 64)
	if err != nil {
		return nil //nolint:nilerr // not a Telegram chat
	}
	switch {
	case ch.Type == TypeTelegramBot && a.Kind == actionTyping:
		if !s.throttle(ctx, "tg:typing:"+a.ConversationID.String()) {
			return nil
		}
		var sec botSecret
		if err := json.Unmarshal(raw, &sec); err != nil {
			return err
		}
		return s.opts.Bots.SendChatAction(ctx, sec.Token, chatID)
	case ch.Type == TypeTelegramUser:
		c := s.liveConn(ch.ID)
		if c == nil {
			return nil
		}
		if a.Kind == actionTyping && !s.throttle(ctx, "tg:typing:"+a.ConversationID.String()) {
			return nil
		}
		if a.Kind == actionRead && th.LastInbound == 0 {
			return nil
		}
		peer, err := s.peer(ctx, a.WorkspaceID, ch.ID, chatID)
		if err != nil {
			return err
		}
		if a.Kind == actionTyping {
			return c.Typing(ctx, peer)
		}
		return c.ReadHistory(ctx, peer, th.LastInbound)
	}
	return nil
}

// throttle lets one caller per typingEvery through for a key.
func (s *Service) throttle(ctx context.Context, key string) bool {
	ok, err := s.opts.Redis.SetNX(ctx, key, "1", typingEvery).Result()
	return err == nil && ok
}

// isOutgoingEcho filters the personal account's stream: an outgoing
// message Tolkovo sent itself is not stored again as the owner's reply.
func (s *Service) isOutgoingEcho(ctx context.Context, channelID uuid.UUID, m telegram.Message) bool {
	return m.Out && s.sentByUs(ctx, channelID, m)
}

// threadOf is the external thread of a private Telegram chat.
func threadOf(chatID int64) string { return strconv.FormatInt(chatID, 10) }
