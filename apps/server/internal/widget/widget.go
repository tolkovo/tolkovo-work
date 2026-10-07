// Package widget serves the visitor's side of the chat widget (docs/04 §6,
// docs/02 §13.2, W-01): a session per device, the visitor's messages, their
// chat and the receipts that tell the operator a reply was seen.
//
// The page carries a public widget key (internal/channels); the server
// answers only pages whose Origin the admin allowed. A session token is 32
// random bytes and the workspace id, so the session is looked up inside
// that tenant's transaction, as ingest keys are. An anonymous visitor gets
// a contact only with the first message: opening a page creates nothing in
// the inbox. A visitor the company's backend signed (W-04) continues with
// the contact of that external id, the same one their app reports land on.
//
// Messages, names and emails are personal data and never reach the logs.
package widget

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"

	"github.com/tolkovo/tolkovo/apps/server/internal/channels"
	"github.com/tolkovo/tolkovo/apps/server/internal/conversations"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/db"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/ratelimit"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/storage"
	"github.com/tolkovo/tolkovo/apps/server/internal/widget/store"
)

// Session lifetime (docs/04 §6.7): 30 days after the last activity.
const (
	SessionTTL = 30 * 24 * time.Hour
	// touchEvery is how often activity is written: once an hour is enough
	// for a 30-day expiry.
	touchEvery = time.Hour
)

// Input bounds.
const (
	MaxMessageChars = conversations.MaxBodyLength
	maxNameChars    = 200
	maxEmailBytes   = 254
	maxKeyBytes     = 255
	sessionPrefix   = "tk_wsess_"
)

// Rates (docs/02 §13.4): sessions per IP, messages per visitor, reads per
// session and IP. The key is public, so everything is bounded.
var (
	sessionsPerIP     = ratelimit.Rule{Limit: 20, Window: time.Minute}
	messagesPerVisit  = ratelimit.Rule{Limit: 30, Window: time.Minute}
	readsPerSession   = ratelimit.Rule{Limit: 60, Window: time.Minute}
	receiptsPerVisit  = ratelimit.Rule{Limit: 60, Window: time.Minute}
	requestsPerIP     = ratelimit.Rule{Limit: 300, Window: time.Minute}
	connectsPerTarget = ratelimit.Rule{Limit: 30, Window: time.Minute}
	introsPerSession  = ratelimit.Rule{Limit: 10, Window: time.Minute}
	checksPerSession  = ratelimit.Rule{Limit: 60, Window: time.Minute}
	ratingsPerSession = ratelimit.Rule{Limit: 10, Window: time.Minute}
)

// Errors of the module.
var (
	ErrInvalid = errors.New("widget: invalid input")
	// ErrInvalidKey: the widget key is malformed or names no active widget.
	ErrInvalidKey = errors.New("widget: invalid widget key")
	// ErrOriginNotAllowed: the page's origin is not on the channel's list.
	ErrOriginNotAllowed = errors.New("widget: origin not allowed")
	// ErrSessionEnded: the token is unknown, expired, ended, or its widget
	// was disconnected. The widget starts a new session.
	ErrSessionEnded = errors.New("widget: session ended")
	// ErrIdempotencyMismatch: the key was used with another text.
	ErrIdempotencyMismatch = errors.New("widget: the Idempotency-Key was used with a different message")
	// ErrNotFound: no such conversation in the visitor's chat.
	ErrNotFound = errors.New("widget: not found")
	// ErrConversationOpen: the conversation to rate is not resolved.
	ErrConversationOpen = errors.New("widget: conversation is not resolved")
)

// RateLimitedError means a rate is used up.
type RateLimitedError struct{ RetryAfter time.Duration }

func (e *RateLimitedError) Error() string {
	return fmt.Sprintf("widget: too many requests, retry after %s", e.RetryAfter)
}

// Channels is what the widget needs of internal/channels.
type Channels interface {
	WidgetByKey(ctx context.Context, key string) (channels.WidgetChannel, error)
	Widget(ctx context.Context, workspaceID, channelID uuid.UUID) (channels.WidgetChannel, error)
	VerifyClaim(ctx context.Context, workspaceID, channelID uuid.UUID, c channels.IdentityClaim) (channels.Identity, error)
	ReceiveVisitorMessage(ctx context.Context, workspaceID uuid.UUID, m channels.VisitorMessage) (conversations.Received, error)
}

// Inbox is what the widget needs of internal/conversations.
type Inbox interface {
	ResolveVisitor(ctx context.Context, workspaceID uuid.UUID, v conversations.Visitor) (conversations.VisitorContact, error)
	VisitorMessages(ctx context.Context, workspaceID uuid.UUID, chat conversations.VisitorChat, since string) (conversations.VisitorMessages, error)
	VisitorMessageByKey(ctx context.Context, workspaceID uuid.UUID, chat conversations.VisitorChat, externalID string) (conversations.VisitorMessage, error)
	MarkVisitorReceipt(ctx context.Context, workspaceID uuid.UUID, chat conversations.VisitorChat, r conversations.VisitorReceipt) error
	AttachedKeys(ctx context.Context, workspaceID uuid.UUID, storageKeys []string) ([]string, error)
	RateVisitorConversation(
		ctx context.Context, workspaceID uuid.UUID, chat conversations.VisitorChat, id uuid.UUID, score int, comment string,
	) (conversations.Rating, error)
	// Topics of questions (docs/02 §7.3).
	Topics(ctx context.Context, workspaceID uuid.UUID) ([]conversations.Topic, error)
	ChooseVisitorTopic(
		ctx context.Context, workspaceID uuid.UUID, chat conversations.VisitorChat, conversationID, topicID uuid.UUID, onlyIfNone bool,
	) error
}

// Presence tells whether someone takes conversations now (internal/routing).
type Presence interface {
	AnyOnline(ctx context.Context, workspaceID uuid.UUID) (bool, error)
}

// Options configure the service.
type Options struct {
	Channels Channels
	Inbox    Inbox
	// Presence tells whether the team is there; without it (tests) the
	// team counts as online.
	Presence Presence
	Redis    *redis.Client
	// Files stores visitors' files (W-02); Scanner, nil without ClamAV,
	// checks them before a message takes them.
	Files   storage.Storage
	Scanner Scanner
	// KeyPrefix separates Redis keys of tests.
	KeyPrefix string
	Log       *slog.Logger
}

// Service is the module facade.
type Service struct {
	db      *db.DB
	opts    Options
	limiter *ratelimit.Limiter
	now     func() time.Time
}

// New returns the widget service.
func New(d *db.DB, opts Options) *Service {
	return &Service{
		db: d, opts: opts, limiter: ratelimit.New(opts.Redis, opts.KeyPrefix),
		now: func() time.Time { return time.Now().UTC() },
	}
}

// Caller is the page that calls: its Origin header and the client's IP.
type Caller struct {
	Origin string
	IP     string
}

// User is who the site says the visitor is (the JS API's identify). With
// UserHash, the company's backend signed ExternalID (W-04); without it,
// everything is a claim.
type User struct {
	ExternalID string
	UserHash   string
	Name       string
	Email      string
}

func (u User) empty() bool {
	return u.ExternalID == "" && u.UserHash == "" && strings.TrimSpace(u.Name) == "" && strings.TrimSpace(u.Email) == ""
}

// Start is a request for a session: the widget key, the token the widget
// kept (if any) and who the visitor is (if the site knows).
type Start struct {
	WidgetKey string
	Token     string
	User      User
}

// Session is the widget's session.
type Session struct {
	// Token is the session's token: the one given if it still works,
	// otherwise a new one. The widget keeps it and sends it from now on.
	Token     string
	ExpiresAt time.Time
	// Identity is conversations.IdentityVerified, IdentityUnverified or
	// IdentityNone.
	Identity    string
	ChannelName string
	// OperatorsOnline: someone takes conversations now; without them the
	// widget offers the offline form (W-02).
	OperatorsOnline bool
	// Analytics is where the widget reports its goals on the site (W-05).
	Analytics channels.WidgetAnalytics
	// Topics the visitor picks from before writing; empty asks none.
	Topics []conversations.Topic
}

// Visitor is an authenticated session.
type Visitor struct {
	WorkspaceID uuid.UUID
	ChannelID   uuid.UUID
	SessionID   uuid.UUID
	// ContactID is uuid.Nil until the first message or a verified identity.
	ContactID uuid.UUID
}

func (v Visitor) chat() conversations.VisitorChat {
	return conversations.VisitorChat{ChannelID: v.ChannelID, Thread: channels.VisitorThread(v.ContactID)}
}

// Start opens a session or continues the one the widget kept. A user the
// company's backend signed switches the session to that user's contact; a
// signed-out visitor's widget ends its session first (End).
func (s *Service) Start(ctx context.Context, c Caller, in Start) (Session, error) {
	if err := s.allow(ctx, "widget:session-ip:"+c.IP, sessionsPerIP); err != nil {
		return Session{}, err
	}
	if err := in.User.validate(); err != nil {
		return Session{}, err
	}
	w, err := s.opts.Channels.WidgetByKey(ctx, in.WidgetKey)
	if errors.Is(err, channels.ErrInvalidWidgetKey) {
		return Session{}, ErrInvalidKey
	}
	if err != nil {
		return Session{}, err
	}
	if !w.Allows(c.Origin) {
		return Session{}, ErrOriginNotAllowed
	}
	cur, token, err := s.continued(ctx, w, in.Token)
	if err != nil {
		return Session{}, err
	}
	var identity channels.Identity
	if !in.User.empty() {
		identity, err = s.opts.Channels.VerifyClaim(ctx, w.WorkspaceID, w.ChannelID, channels.IdentityClaim{
			ExternalID: in.User.ExternalID, Email: in.User.Email, Name: in.User.Name, UserHash: in.User.UserHash,
		})
		if errors.Is(err, channels.ErrInvalid) {
			return Session{}, fmt.Errorf("%w: %s", ErrInvalid, strings.TrimPrefix(err.Error(), channels.ErrInvalid.Error()+": "))
		}
		if err != nil {
			return Session{}, err
		}
	}

	contact := uuid.Nil
	if cur != nil && cur.ContactID != nil {
		contact = *cur.ContactID
	}
	out := Session{ChannelName: w.Name, Identity: conversations.IdentityNone, Analytics: w.Analytics}
	switch {
	case identity.Verified || (contact != uuid.Nil && !in.User.empty()):
		// A signed user, or a claim on a session that has a contact: the
		// contact module decides which contact goes on (W-04).
		vc, err := s.opts.Inbox.ResolveVisitor(ctx, w.WorkspaceID, conversations.Visitor{
			Current: contact, Verified: identity.Verified, ExternalID: in.User.ExternalID,
			Email: in.User.Email, Name: in.User.Name,
		})
		if err != nil {
			return Session{}, s.inboxError(err)
		}
		contact, out.Identity = vc.ContactID, vc.Identity
	case contact != uuid.Nil:
		vc, err := s.opts.Inbox.ResolveVisitor(ctx, w.WorkspaceID, conversations.Visitor{Current: contact})
		if err != nil {
			return Session{}, s.inboxError(err)
		}
		contact, out.Identity = vc.ContactID, vc.Identity
	case !in.User.empty():
		// An anonymous visitor's claim waits for the first message.
		out.Identity = conversations.IdentityUnverified
	case cur != nil && cur.Claimed:
		out.Identity = conversations.IdentityUnverified
	}

	now := s.now()
	out.ExpiresAt = now.Add(SessionTTL)
	err = s.db.InTenantTx(ctx, w.WorkspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		if cur == nil {
			id, err := uuid.NewV7()
			if err != nil {
				return err
			}
			var hash []byte
			token, hash, err = newToken(w.WorkspaceID)
			if err != nil {
				return err
			}
			p := store.InsertSessionParams{
				ID: id, WorkspaceID: w.WorkspaceID, ChannelID: w.ChannelID, TokenHash: hash, Now: now, ExpiresAt: out.ExpiresAt,
			}
			if contact != uuid.Nil {
				p.ContactID = &contact
			} else if !in.User.empty() {
				p.Claimed, p.ClaimName, p.ClaimEmail = true, in.User.claimName(), in.User.claimEmail()
			}
			_, err = q.InsertSession(ctx, p)
			return err
		}
		switch {
		case contact != uuid.Nil && (cur.ContactID == nil || *cur.ContactID != contact):
			if _, err := q.SetSessionContact(ctx, store.SetSessionContactParams{WorkspaceID: w.WorkspaceID, ID: cur.ID, ContactID: &contact}); err != nil {
				return err
			}
		case contact == uuid.Nil && !in.User.empty():
			if _, err := q.SetSessionClaim(ctx, store.SetSessionClaimParams{
				WorkspaceID: w.WorkspaceID, ID: cur.ID, ClaimName: in.User.claimName(), ClaimEmail: in.User.claimEmail(),
			}); err != nil {
				return err
			}
		}
		return q.TouchSession(ctx, store.TouchSessionParams{WorkspaceID: w.WorkspaceID, ID: cur.ID, Now: now, ExpiresAt: out.ExpiresAt})
	})
	if err != nil {
		return Session{}, fmt.Errorf("widget: save session: %w", err)
	}
	out.Token = token
	if out.OperatorsOnline, err = s.anyOnline(ctx, w.WorkspaceID); err != nil {
		return Session{}, err
	}
	if out.Topics, err = s.opts.Inbox.Topics(ctx, w.WorkspaceID); err != nil {
		return Session{}, err
	}
	return out, nil
}

// OperatorsOnline tells the widget of a session whether someone takes
// conversations now, asked again when the visitor opens the window.
func (s *Service) OperatorsOnline(ctx context.Context, c Caller, token string) (bool, error) {
	if err := s.allow(ctx, "widget:ip:"+c.IP, requestsPerIP); err != nil {
		return false, err
	}
	v, err := s.Authenticate(ctx, c, token)
	if err != nil {
		return false, err
	}
	if err := s.allow(ctx, "widget:checks:"+v.SessionID.String(), checksPerSession); err != nil {
		return false, err
	}
	return s.anyOnline(ctx, v.WorkspaceID)
}

func (s *Service) anyOnline(ctx context.Context, workspaceID uuid.UUID) (bool, error) {
	if s.opts.Presence == nil {
		return true, nil
	}
	return s.opts.Presence.AnyOnline(ctx, workspaceID)
}

// continued returns the session of a token the widget kept, if it belongs
// to this widget and still works; nil and "" otherwise.
func (s *Service) continued(ctx context.Context, w channels.WidgetChannel, token string) (*store.WidgetSession, string, error) {
	if token == "" {
		return nil, "", nil
	}
	ws, ok := parseToken(token)
	if !ok || ws != w.WorkspaceID {
		return nil, "", nil
	}
	sess, err := s.session(ctx, ws, token)
	if errors.Is(err, ErrSessionEnded) || (err == nil && sess.ChannelID != w.ChannelID) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	return &sess, token, nil
}

// session finds a live session by its token in its workspace.
func (s *Service) session(ctx context.Context, workspaceID uuid.UUID, token string) (store.WidgetSession, error) {
	hash := sha256.Sum256([]byte(token))
	var sess store.WidgetSession
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		var err error
		sess, err = store.New(tx).SessionByHash(ctx, store.SessionByHashParams{WorkspaceID: workspaceID, TokenHash: hash[:]})
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !sess.ExpiresAt.After(s.now())) {
		return store.WidgetSession{}, ErrSessionEnded
	}
	if err != nil {
		return store.WidgetSession{}, fmt.Errorf("widget: load session: %w", err)
	}
	return sess, nil
}

// Authenticate checks a session token from a page of origin: the session
// is live, its widget is connected and still allows the origin. Activity
// extends the session.
func (s *Service) Authenticate(ctx context.Context, c Caller, token string) (Visitor, error) {
	ws, ok := parseToken(token)
	if !ok {
		return Visitor{}, ErrSessionEnded
	}
	sess, err := s.session(ctx, ws, token)
	if err != nil {
		return Visitor{}, err
	}
	w, err := s.opts.Channels.Widget(ctx, ws, sess.ChannelID)
	if errors.Is(err, channels.ErrNotFound) {
		return Visitor{}, ErrSessionEnded
	}
	if err != nil {
		return Visitor{}, err
	}
	if !w.Allows(c.Origin) {
		return Visitor{}, ErrOriginNotAllowed
	}
	if now := s.now(); now.Sub(sess.LastSeenAt) >= touchEvery {
		err := s.db.InTenantTx(ctx, ws, func(tx pgx.Tx) error {
			return store.New(tx).TouchSession(ctx, store.TouchSessionParams{
				WorkspaceID: ws, ID: sess.ID, Now: now, ExpiresAt: now.Add(SessionTTL),
			})
		})
		if err != nil {
			return Visitor{}, fmt.Errorf("widget: touch session: %w", err)
		}
	}
	v := Visitor{WorkspaceID: ws, ChannelID: sess.ChannelID, SessionID: sess.ID}
	if sess.ContactID != nil {
		v.ContactID = *sess.ContactID
	}
	return v, nil
}

// End ends a session: the widget calls it when the visitor signs out of
// the site or the app, so the next visitor on the device starts afresh.
// Ending an ended session changes nothing.
func (s *Service) End(ctx context.Context, c Caller, token string) error {
	v, err := s.Authenticate(ctx, c, token)
	if errors.Is(err, ErrSessionEnded) {
		return nil
	}
	if err != nil {
		return err
	}
	err = s.db.InTenantTx(ctx, v.WorkspaceID, func(tx pgx.Tx) error {
		return store.New(tx).DeleteSession(ctx, store.DeleteSessionParams{WorkspaceID: v.WorkspaceID, ID: v.SessionID})
	})
	if err != nil {
		return fmt.Errorf("widget: end session: %w", err)
	}
	return nil
}

// Sent is a stored message of the visitor.
type Sent struct {
	conversations.VisitorMessage
	// Created is false when the key named an earlier message.
	Created bool
}

// Message is what the visitor sends: text, files they uploaded
// (CreateUpload) or both, and the Idempotency-Key.
type Message struct {
	Key   string
	Body  string
	Files []uuid.UUID
	// TopicID is the topic the visitor picked before writing; it goes to a
	// conversation without one.
	TopicID *uuid.UUID
}

// Send stores what the visitor wrote. The first message of an anonymous
// session creates its contact, with what the visitor claimed before. A
// retry with the same key and text returns the first message.
func (s *Service) Send(ctx context.Context, c Caller, token string, m Message) (Sent, error) {
	if err := s.allow(ctx, "widget:ip:"+c.IP, requestsPerIP); err != nil {
		return Sent{}, err
	}
	body, key := strings.TrimSpace(m.Body), m.Key
	switch {
	case (body == "" && len(m.Files) == 0) || utf8.RuneCountInString(body) > MaxMessageChars || !utf8.ValidString(body):
		return Sent{}, fmt.Errorf("%w: message must be 1 to %d characters, or files", ErrInvalid, MaxMessageChars)
	case len(key) > maxKeyBytes:
		return Sent{}, fmt.Errorf("%w: Idempotency-Key must be at most %d characters", ErrInvalid, maxKeyBytes)
	}
	if err := validUploads(m.Files); err != nil {
		return Sent{}, err
	}
	v, err := s.Authenticate(ctx, c, token)
	if err != nil {
		return Sent{}, err
	}
	if err := s.allow(ctx, "widget:messages:"+v.SessionID.String(), messagesPerVisit); err != nil {
		return Sent{}, err
	}
	if key == "" {
		key = uuid.NewString()
	}
	files, err := s.takeUploads(ctx, v, key, m.Files)
	if err != nil {
		return Sent{}, err
	}
	if v.ContactID == uuid.Nil {
		if v.ContactID, err = s.firstContact(ctx, v); err != nil {
			return Sent{}, err
		}
	}
	res, err := s.opts.Channels.ReceiveVisitorMessage(ctx, v.WorkspaceID, channels.VisitorMessage{
		ChannelID: v.ChannelID, ContactID: v.ContactID, Key: key, Body: body, Files: files,
	})
	if err != nil {
		return Sent{}, s.inboxError(err)
	}
	msg, err := s.opts.Inbox.VisitorMessageByKey(ctx, v.WorkspaceID, v.chat(), channels.VisitorExternalID(key))
	if err != nil {
		return Sent{}, s.inboxError(err)
	}
	if !res.Stored && msg.Body != body {
		return Sent{}, ErrIdempotencyMismatch
	}
	if res.Stored && m.TopicID != nil {
		// The message is stored: a topic that is gone is no reason to fail it.
		err := s.opts.Inbox.ChooseVisitorTopic(ctx, v.WorkspaceID, v.chat(), res.ConversationID, *m.TopicID, true)
		if err != nil && !errors.Is(err, conversations.ErrNotFound) {
			s.log().Warn("widget topic not kept", "workspace_id", v.WorkspaceID, "error", err)
		}
	}
	return Sent{VisitorMessage: msg, Created: res.Stored}, nil
}

// firstContact gives an anonymous session its contact. Two first messages
// at once may both create one; the session keeps the first, the other
// stays an empty contact without conversations.
func (s *Service) firstContact(ctx context.Context, v Visitor) (uuid.UUID, error) {
	var sess store.WidgetSession
	err := s.db.InTenantTx(ctx, v.WorkspaceID, func(tx pgx.Tx) error {
		var err error
		sess, err = store.New(tx).GetSession(ctx, store.GetSessionParams{WorkspaceID: v.WorkspaceID, ID: v.SessionID})
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrSessionEnded
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("widget: load session: %w", err)
	}
	if sess.ContactID != nil {
		return *sess.ContactID, nil
	}
	var consent *conversations.Consent
	if sess.ClaimConsent != nil {
		consent = new(conversations.Consent)
		if err := json.Unmarshal(sess.ClaimConsent, consent); err != nil {
			return uuid.Nil, fmt.Errorf("widget: session consent: %w", err)
		}
	}
	vc, err := s.opts.Inbox.ResolveVisitor(ctx, v.WorkspaceID, conversations.Visitor{
		Claimed: sess.Claimed, Name: deref(sess.ClaimName), Email: deref(sess.ClaimEmail),
		Phone: deref(sess.ClaimPhone), Consent: consent,
	})
	if err != nil {
		return uuid.Nil, s.inboxError(err)
	}
	err = s.db.InTenantTx(ctx, v.WorkspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		_, err := q.AttachSessionContact(ctx, store.AttachSessionContactParams{WorkspaceID: v.WorkspaceID, ID: v.SessionID, ContactID: &vc.ContactID})
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		sess, err = q.GetSession(ctx, store.GetSessionParams{WorkspaceID: v.WorkspaceID, ID: v.SessionID})
		if err == nil && sess.ContactID != nil {
			vc.ContactID = *sess.ContactID
		}
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrSessionEnded
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("widget: attach contact: %w", err)
	}
	return vc.ContactID, nil
}

// Intro is what the visitor typed into the widget's pre-chat form
// (docs/04 §6.3). Which fields the form asks for is the site's choice, set
// in the widget's code.
type Intro struct {
	Name  string
	Email string
	Phone string
	// PolicyURL: the visitor ticked the consent to the processing of
	// personal data, shown with a link to this policy.
	PolicyURL string
	// Offline: the offline form, not the pre-chat one; the consent says so.
	Offline bool
}

// Introduce keeps what the visitor said about themselves. A session
// without a contact keeps it until the first message, as it keeps an
// identify claim; otherwise it goes onto the contact at once. Typed values
// are claims: the contact becomes unverified and is never merged; a
// verified contact keeps its values and takes only the consent.
func (s *Service) Introduce(ctx context.Context, c Caller, token string, in Intro) error {
	if err := s.allow(ctx, "widget:ip:"+c.IP, requestsPerIP); err != nil {
		return err
	}
	in, err := in.validate()
	if err != nil {
		return err
	}
	v, err := s.Authenticate(ctx, c, token)
	if err != nil {
		return err
	}
	if err := s.allow(ctx, "widget:intros:"+v.SessionID.String(), introsPerSession); err != nil {
		return err
	}
	var consent *conversations.Consent
	if in.PolicyURL != "" {
		source := "widget_prechat"
		if in.Offline {
			source = "widget_offline"
		}
		consent = &conversations.Consent{PolicyURL: in.PolicyURL, GivenAt: s.now(), Source: source}
	}
	if v.ContactID == uuid.Nil {
		kept, contact, err := s.keepIntro(ctx, v, in, consent)
		if err != nil || kept {
			return err
		}
		// A first message gave the session its contact meanwhile.
		v.ContactID = contact
	}
	vc, err := s.opts.Inbox.ResolveVisitor(ctx, v.WorkspaceID, conversations.Visitor{
		Current: v.ContactID, Name: in.Name, Email: in.Email, Phone: in.Phone, Consent: consent,
	})
	if err != nil {
		return s.inboxError(err)
	}
	if vc.ContactID == v.ContactID {
		return nil
	}
	// The session's contact was deleted: the session goes on with the new one.
	err = s.db.InTenantTx(ctx, v.WorkspaceID, func(tx pgx.Tx) error {
		_, err := store.New(tx).SetSessionContact(ctx, store.SetSessionContactParams{WorkspaceID: v.WorkspaceID, ID: v.SessionID, ContactID: &vc.ContactID})
		return err
	})
	if err != nil {
		return fmt.Errorf("widget: save session: %w", err)
	}
	return nil
}

// keepIntro adds the form to the claims of a session without a contact.
// kept is false when the session has a contact by now; contact is that one.
func (s *Service) keepIntro(ctx context.Context, v Visitor, in Intro, consent *conversations.Consent) (kept bool, contact uuid.UUID, err error) {
	p := store.AddSessionIntroParams{
		WorkspaceID: v.WorkspaceID, ID: v.SessionID,
		ClaimName: optional(in.Name), ClaimEmail: optional(in.Email), ClaimPhone: optional(in.Phone),
	}
	if consent != nil {
		if p.ClaimConsent, err = json.Marshal(consent); err != nil {
			return false, uuid.Nil, err
		}
	}
	err = s.db.InTenantTx(ctx, v.WorkspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		_, err := q.AddSessionIntro(ctx, p)
		if err == nil {
			kept = true
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		sess, err := q.GetSession(ctx, store.GetSessionParams{WorkspaceID: v.WorkspaceID, ID: v.SessionID})
		if err != nil {
			return err
		}
		if sess.ContactID != nil {
			contact = *sess.ContactID
		}
		return nil
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, uuid.Nil, ErrSessionEnded
	}
	if err != nil {
		return false, uuid.Nil, fmt.Errorf("widget: keep intro: %w", err)
	}
	return kept, contact, nil
}

// validate trims the form and checks it; messages name the field, never
// the value.
func (in Intro) validate() (Intro, error) {
	in = Intro{
		Name: strings.TrimSpace(in.Name), Email: strings.TrimSpace(in.Email),
		Phone: strings.TrimSpace(in.Phone), PolicyURL: strings.TrimSpace(in.PolicyURL), Offline: in.Offline,
	}
	switch {
	case in.Name == "" && in.Email == "" && in.Phone == "" && in.PolicyURL == "":
		return in, fmt.Errorf("%w: give a name, an email, a phone or the consent", ErrInvalid)
	case utf8.RuneCountInString(in.Name) > maxNameChars || !utf8.ValidString(in.Name):
		return in, fmt.Errorf("%w: name must be at most %d characters", ErrInvalid, maxNameChars)
	case in.Email != "" && !conversations.ValidEmail(in.Email):
		return in, fmt.Errorf("%w: email is not a valid address", ErrInvalid)
	case in.Phone != "" && !conversations.ValidPhone(in.Phone):
		return in, fmt.Errorf("%w: phone must be 3-32 digits, spaces, brackets or hyphens", ErrInvalid)
	case in.PolicyURL != "" && !validPolicyURL(in.PolicyURL):
		return in, fmt.Errorf("%w: consent.policy_url must be an http(s) URL of at most %d bytes", ErrInvalid, conversations.MaxPolicyURLLength)
	}
	return in, nil
}

func validPolicyURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && len(raw) <= conversations.MaxPolicyURLLength && (u.Scheme == "https" || u.Scheme == "http") && u.Host != ""
}

func optional(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}

// Messages returns the visitor's chat after since; without since, its
// newest messages. A session without a contact has none.
func (s *Service) Messages(ctx context.Context, c Caller, token, since string) (conversations.VisitorMessages, error) {
	if err := s.allow(ctx, "widget:ip:"+c.IP, requestsPerIP); err != nil {
		return conversations.VisitorMessages{}, err
	}
	v, err := s.Authenticate(ctx, c, token)
	if err != nil {
		return conversations.VisitorMessages{}, err
	}
	if err := s.allow(ctx, "widget:reads:"+v.SessionID.String(), readsPerSession); err != nil {
		return conversations.VisitorMessages{}, err
	}
	if v.ContactID == uuid.Nil {
		return conversations.VisitorMessages{Items: []conversations.VisitorMessage{}, Cursor: since}, nil
	}
	page, err := s.opts.Inbox.VisitorMessages(ctx, v.WorkspaceID, v.chat(), since)
	if errors.Is(err, conversations.ErrInvalid) {
		return conversations.VisitorMessages{}, fmt.Errorf("%w: since is not a cursor of this API", ErrInvalid)
	}
	return page, err
}

// Receipt records that the widget received and showed replies.
func (s *Service) Receipt(ctx context.Context, c Caller, token string, r conversations.VisitorReceipt) error {
	if err := s.allow(ctx, "widget:ip:"+c.IP, requestsPerIP); err != nil {
		return err
	}
	if r.DeliveredUpTo == uuid.Nil && r.ReadUpTo == uuid.Nil {
		return fmt.Errorf("%w: give delivered_up_to, read_up_to or both", ErrInvalid)
	}
	v, err := s.Authenticate(ctx, c, token)
	if err != nil {
		return err
	}
	if err := s.allow(ctx, "widget:receipts:"+v.SessionID.String(), receiptsPerVisit); err != nil {
		return err
	}
	if v.ContactID == uuid.Nil {
		return nil
	}
	return s.opts.Inbox.MarkVisitorReceipt(ctx, v.WorkspaceID, v.chat(), r)
}

// Rate stores the visitor's rating of a resolved conversation of their
// chat (W-05).
func (s *Service) Rate(ctx context.Context, c Caller, token string, conversationID uuid.UUID, score int, comment string) (conversations.Rating, error) {
	if err := s.allow(ctx, "widget:ip:"+c.IP, requestsPerIP); err != nil {
		return conversations.Rating{}, err
	}
	v, err := s.Authenticate(ctx, c, token)
	if err != nil {
		return conversations.Rating{}, err
	}
	if err := s.allow(ctx, "widget:ratings:"+v.SessionID.String(), ratingsPerSession); err != nil {
		return conversations.Rating{}, err
	}
	if v.ContactID == uuid.Nil {
		return conversations.Rating{}, ErrNotFound
	}
	r, err := s.opts.Inbox.RateVisitorConversation(ctx, v.WorkspaceID, v.chat(), conversationID, score, comment)
	switch {
	case errors.Is(err, conversations.ErrNotFound):
		return conversations.Rating{}, ErrNotFound
	case errors.Is(err, conversations.ErrConversationOpen):
		return conversations.Rating{}, ErrConversationOpen
	case errors.Is(err, conversations.ErrInvalid):
		return conversations.Rating{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return r, err
}

// ChooseTopic records the topic the visitor picked for an open
// conversation of their chat.
func (s *Service) ChooseTopic(ctx context.Context, c Caller, token string, conversationID, topicID uuid.UUID) error {
	if err := s.allow(ctx, "widget:ip:"+c.IP, requestsPerIP); err != nil {
		return err
	}
	v, err := s.Authenticate(ctx, c, token)
	if err != nil {
		return err
	}
	if err := s.allow(ctx, "widget:topics:"+v.SessionID.String(), ratingsPerSession); err != nil {
		return err
	}
	if v.ContactID == uuid.Nil {
		return ErrNotFound
	}
	err = s.opts.Inbox.ChooseVisitorTopic(ctx, v.WorkspaceID, v.chat(), conversationID, topicID, false)
	if errors.Is(err, conversations.ErrNotFound) {
		return ErrNotFound
	}
	return err
}

// AllowConnect takes one realtime handshake of a client IP. A widget
// reconnects with backoff, so only a broken or hostile client hits it.
func (s *Service) AllowConnect(ctx context.Context, ip string) error {
	return s.allow(ctx, "widget:connect-ip:"+ip, connectsPerTarget)
}

func (s *Service) allow(ctx context.Context, key string, rule ratelimit.Rule) error {
	res, err := s.limiter.Hit(ctx, key, rule)
	if err != nil {
		return err
	}
	if !res.Allowed {
		return &RateLimitedError{RetryAfter: res.Reset}
	}
	return nil
}

// inboxError maps the contact and conversation errors a visitor can cause.
func (s *Service) inboxError(err error) error {
	switch {
	case errors.Is(err, conversations.ErrInvalid), errors.Is(err, channels.ErrInvalid):
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	case errors.Is(err, conversations.ErrNotFound), errors.Is(err, channels.ErrNotFound):
		// The contact was deleted with the session's chat: start afresh.
		return ErrSessionEnded
	}
	return err
}

func (u User) validate() error {
	switch {
	case len(u.ExternalID) > channels.MaxExternalIDLength || !utf8.ValidString(u.ExternalID):
		return fmt.Errorf("%w: user.external_id must be valid text of at most %d bytes", ErrInvalid, channels.MaxExternalIDLength)
	case len(u.Email) > maxEmailBytes || !utf8.ValidString(u.Email):
		return fmt.Errorf("%w: user.email must be at most %d bytes", ErrInvalid, maxEmailBytes)
	case utf8.RuneCountInString(u.Name) > maxNameChars || !utf8.ValidString(u.Name):
		return fmt.Errorf("%w: user.name must be at most %d characters", ErrInvalid, maxNameChars)
	case len(u.UserHash) > 128:
		return fmt.Errorf("%w: user.user_hash is too long", ErrInvalid)
	}
	return nil
}

func (u User) claimName() *string {
	if n := strings.TrimSpace(u.Name); n != "" {
		return &n
	}
	return nil
}

func (u User) claimEmail() *string {
	if e := strings.TrimSpace(u.Email); e != "" {
		return &e
	}
	return nil
}

// newToken returns a session token and its hash.
func newToken(workspaceID uuid.UUID) (string, []byte, error) {
	b := make([]byte, 32, 48)
	if _, err := rand.Read(b); err != nil {
		return "", nil, fmt.Errorf("random: %w", err)
	}
	b = append(b, workspaceID[:]...)
	token := sessionPrefix + base64.RawURLEncoding.EncodeToString(b)
	hash := sha256.Sum256([]byte(token))
	return token, hash[:], nil
}

// parseToken returns the workspace a token names.
func parseToken(token string) (uuid.UUID, bool) {
	rest, ok := strings.CutPrefix(token, sessionPrefix)
	if !ok {
		return uuid.Nil, false
	}
	b, err := base64.RawURLEncoding.Strict().DecodeString(rest)
	if err != nil || len(b) != 48 {
		return uuid.Nil, false
	}
	ws, err := uuid.FromBytes(b[32:])
	if err != nil || ws == uuid.Nil {
		return uuid.Nil, false
	}
	return ws, true
}

func (s *Service) log() *slog.Logger {
	if s.opts.Log != nil {
		return s.opts.Log
	}
	return slog.Default()
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
