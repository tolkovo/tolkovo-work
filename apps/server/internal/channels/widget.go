package channels

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tolkovo/tolkovo/apps/server/internal/channels/store"
	"github.com/tolkovo/tolkovo/apps/server/internal/conversations"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/crypto"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/outbox"
)

// The «Виджет» channel (W-01, docs/02 §13.2): a chat on a company's site or
// inside its app (an Electron renderer). The page embeds a public widget
// key; the server answers only pages whose Origin the admin allowed. A
// visitor's chat is the thread of their contact id, as a Telegram chat is
// the thread of the user id; the widget module (internal/widget) issues
// their sessions and resolves the contact (W-04).

// TypeWidget is a chat on a site or in an app.
const TypeWidget = "widget"

// MaxAllowedOrigins bounds the origins of one widget channel.
const MaxAllowedOrigins = 20

// TopicWidgetMessage is the outbox event that tells a visitor's widget a
// reply is there to fetch. It is routed to the visitor's own realtime
// connections, never to the panel (internal/realtime).
const TopicWidgetMessage = "widget.message"

// widgetKeyPrefix starts a widget key. The key is base64url of the
// workspace and channel ids: it is public by design (it sits in the page),
// so it carries no secret, and the session API learns the tenant from it
// without a policy that sees every workspace.
const widgetKeyPrefix = "tk_widget_"

// Special origins an admin may allow on purpose (docs/02 §13.2). Electron
// renderers loaded from file:// send "null" on fetch and "file://" on
// WebSocket handshakes, depending on the Chromium version. "null" is also
// what a sandboxed iframe on any site sends, so allowing it lets any page
// embed the chat; a custom scheme (app://name) is the safer choice.
const (
	OriginNull = "null"
	OriginFile = "file://"
)

// ErrInvalidWidgetKey means the widget key is malformed or names no active
// widget channel. The session API does not say which.
var ErrInvalidWidgetKey = errors.New("channels: invalid widget key")

// WidgetChannel is what the widget's public API needs of its channel.
type WidgetChannel struct {
	WorkspaceID    uuid.UUID
	ChannelID      uuid.UUID
	Name           string
	AllowedOrigins []string
}

// Allows reports whether a page of origin may use the widget: only the
// origins the admin listed, nothing by default. An empty origin (no header,
// not a browser) is never allowed.
func (w WidgetChannel) Allows(origin string) bool {
	return OriginAllowed(w.AllowedOrigins, origin)
}

// WidgetKey is the public key of a widget channel.
func WidgetKey(workspaceID, channelID uuid.UUID) string {
	b := make([]byte, 0, 32)
	b = append(b, workspaceID[:]...)
	b = append(b, channelID[:]...)
	return widgetKeyPrefix + base64.RawURLEncoding.EncodeToString(b)
}

func parseWidgetKey(key string) (workspaceID, channelID uuid.UUID, ok bool) {
	rest, found := strings.CutPrefix(key, widgetKeyPrefix)
	if !found {
		return uuid.Nil, uuid.Nil, false
	}
	b, err := base64.RawURLEncoding.Strict().DecodeString(rest)
	if err != nil || len(b) != 32 {
		return uuid.Nil, uuid.Nil, false
	}
	ws, _ := uuid.FromBytes(b[:16])
	ch, _ := uuid.FromBytes(b[16:])
	return ws, ch, ws != uuid.Nil && ch != uuid.Nil
}

// WidgetChannelCreated is a new widget channel with its identity secret,
// shown only once.
type WidgetChannelCreated struct {
	Channel        Channel
	IdentitySecret crypto.Secret
}

// ConnectWidget creates a widget channel with the secret that signs a
// visitor's identity (W-04), sealed like the app channel's, so it needs
// TOLKOVO_MASTER_KEY. It is active at once and answers only the origins
// given: none means the widget works nowhere yet.
func (s *Service) ConnectWidget(ctx context.Context, workspaceID, actorID uuid.UUID, name string, origins []string) (WidgetChannelCreated, error) {
	if s.opts.Sealer == nil {
		return WidgetChannelCreated{}, ErrNoEncryption
	}
	name, err := validName(name)
	if err != nil {
		return WidgetChannelCreated{}, err
	}
	origins, err = NormalizeOrigins(origins)
	if err != nil {
		return WidgetChannelCreated{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return WidgetChannelCreated{}, err
	}
	identity, err := newIdentitySecret()
	if err != nil {
		return WidgetChannelCreated{}, err
	}
	sealed, err := s.seal(ctx, workspaceID, id, appSecret{IdentitySecret: identity.Reveal()})
	if err != nil {
		return WidgetChannelCreated{}, err
	}
	cfg, err := json.Marshal(Details{AllowedOrigins: origins})
	if err != nil {
		return WidgetChannelCreated{}, err
	}
	var out WidgetChannelCreated
	now := s.now()
	err = s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		row, err := q.InsertChannel(ctx, store.InsertChannelParams{
			ID: id, WorkspaceID: workspaceID, Type: TypeWidget, Name: name, Status: StatusActive,
			Config: cfg, CreatedBy: &actorID, CreatedAt: now,
		})
		if err != nil {
			return err
		}
		out = WidgetChannelCreated{Channel: channelFrom(row), IdentitySecret: identity}
		return q.PutSecret(ctx, store.PutSecretParams{WorkspaceID: workspaceID, ChannelID: id, Sealed: sealed, UpdatedAt: now})
	})
	if err != nil {
		return WidgetChannelCreated{}, fmt.Errorf("connect widget: %w", err)
	}
	return out, nil
}

// SetWidgetOrigins replaces the origins a widget channel answers. Pages of
// a removed origin lose their sessions within the next re-check.
func (s *Service) SetWidgetOrigins(ctx context.Context, workspaceID, channelID uuid.UUID, origins []string) (Channel, error) {
	origins, err := NormalizeOrigins(origins)
	if err != nil {
		return Channel{}, err
	}
	var ch Channel
	err = s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		row, err := q.GetChannelForUpdate(ctx, store.GetChannelForUpdateParams{WorkspaceID: workspaceID, ID: channelID})
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && row.Type != TypeWidget) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		details := channelFrom(row).Details
		details.AllowedOrigins = origins
		cfg, err := json.Marshal(details)
		if err != nil {
			return err
		}
		row, err = q.UpdateChannelConfig(ctx, store.UpdateChannelConfigParams{
			WorkspaceID: workspaceID, ID: channelID, Config: cfg, UpdatedAt: s.now(),
		})
		ch = channelFrom(row)
		return err
	})
	if err != nil {
		return Channel{}, fmt.Errorf("set widget origins: %w", err)
	}
	return ch, nil
}

// WidgetByKey finds the active widget channel of a public key. The key
// names its workspace, so the channel is read inside that tenant's
// transaction; a forged workspace finds nothing. Any failure to find it is
// ErrInvalidWidgetKey.
func (s *Service) WidgetByKey(ctx context.Context, key string) (WidgetChannel, error) {
	workspaceID, channelID, ok := parseWidgetKey(key)
	if !ok {
		return WidgetChannel{}, ErrInvalidWidgetKey
	}
	w, err := s.Widget(ctx, workspaceID, channelID)
	if errors.Is(err, ErrNotFound) {
		return WidgetChannel{}, ErrInvalidWidgetKey
	}
	return w, err
}

// Widget returns an active widget channel; ErrNotFound for a disconnected
// one, one in another workspace or a channel of another type.
func (s *Service) Widget(ctx context.Context, workspaceID, channelID uuid.UUID) (WidgetChannel, error) {
	var row store.Channel
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		var err error
		row, err = store.New(tx).GetActiveChannel(ctx, store.GetActiveChannelParams{WorkspaceID: workspaceID, ID: channelID})
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && row.Type != TypeWidget) {
			return ErrNotFound
		}
		return err
	})
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return WidgetChannel{}, ErrNotFound
		}
		return WidgetChannel{}, fmt.Errorf("load widget channel: %w", err)
	}
	ch := channelFrom(row)
	return WidgetChannel{WorkspaceID: workspaceID, ChannelID: channelID, Name: ch.Name, AllowedOrigins: ch.Details.AllowedOrigins}, nil
}

// VisitorMessage is what a visitor wrote in the widget.
type VisitorMessage struct {
	ChannelID uuid.UUID
	// ContactID is the visitor's contact (internal/widget resolves it from
	// the session); their chat in the channel is the thread of that id.
	ContactID uuid.UUID
	// Key names the message: a retry with the same key is not stored twice.
	Key  string
	Body string
	// Files are in storage already, checked by internal/widget.
	Files []conversations.ReceivedFile
}

// ReceiveVisitorMessage stores a visitor's message like any channel's
// (docs/02 §11.2): the open conversation of the visitor's thread or a new
// one in the queue. A message seen before is not stored again (Stored
// false).
func (s *Service) ReceiveVisitorMessage(ctx context.Context, workspaceID uuid.UUID, m VisitorMessage) (conversations.Received, error) {
	if m.Key == "" || len(m.Key) > 255 {
		return conversations.Received{}, fmt.Errorf("%w: a message needs a key of at most 255 bytes", ErrInvalid)
	}
	res, err := s.opts.Inbox.Receive(ctx, workspaceID, conversations.Inbound{
		ChannelID: m.ChannelID, Thread: VisitorThread(m.ContactID), ExternalID: VisitorExternalID(m.Key),
		ContactID: m.ContactID, Body: m.Body, Files: m.Files,
	})
	if err != nil {
		return conversations.Received{}, err
	}
	if res.Stored {
		s.markInbound(ctx, workspaceID, m.ChannelID)
	}
	return res, nil
}

// VisitorThread is the thread of a visitor's chat in a widget channel: the
// id of their contact, so a verified visitor finds the same chat on every
// device.
func VisitorThread(contactID uuid.UUID) string { return contactID.String() }

// VisitorExternalID is the external id of a visitor's message with key: the
// prefix keeps the visitor's keys apart from anything else in the thread.
func VisitorExternalID(key string) string { return "msg:" + key }

// queueWidgetPush tells the visitor's widget, in the transaction that
// stores the reply, that it is there: the widget fetches it over REST. The
// event goes only to the visitor's own realtime connections
// (internal/realtime), keyed by the thread, which is their contact id.
func queueWidgetPush(ctx context.Context, tx pgx.Tx, r conversations.QueuedReply) error {
	return outbox.Add(ctx, tx, r.WorkspaceID, TopicWidgetMessage, map[string]any{
		"contact_id": r.Thread, "conversation_id": r.ConversationID, "message_id": r.MessageID,
	})
}

// VisitorSignals shows a visitor that an operator is typing;
// *realtime.Publisher implements it. Best effort, like every signal.
type VisitorSignals interface {
	VisitorTyping(ctx context.Context, workspaceID, contactID uuid.UUID) error
}

// widgetTyping relays an operator's typing to the visitor of a widget
// conversation, at most once per typingEvery.
func (s *Service) widgetTyping(ctx context.Context, a action, thread string) error {
	contactID, err := uuid.Parse(thread)
	if err != nil || s.opts.Visitors == nil || !s.throttle(ctx, "widget:typing:"+a.ConversationID.String()) {
		return nil //nolint:nilerr // not a visitor's thread: nothing to show
	}
	return s.opts.Visitors.VisitorTyping(ctx, a.WorkspaceID, contactID)
}

// Origin forms (docs/02 §13.2).
var (
	// schemeRe is a URL scheme as RFC 3986 allows it.
	schemeRe = regexp.MustCompile(`^[a-z][a-z0-9+.-]*$`)
	// labelRe is one DNS label (or a host name of a custom scheme).
	labelRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
)

// NormalizeOrigins checks the origins an admin allows and returns them in a
// canonical form, without duplicates. Allowed forms:
//
//   - https://shop.example.com, http://localhost:5173 — one site (default
//     ports dropped, as browsers send them);
//   - https://*.example.com — every subdomain of example.com, not the apex;
//   - app://zerno — a custom scheme of a desktop app (Electron's
//     protocol.registerSchemesAsPrivileged with standard: true);
//   - file:// and null — an Electron renderer loaded from a file. Both are
//     explicit: nothing matches by default, and "*" is refused.
func NormalizeOrigins(origins []string) ([]string, error) {
	if len(origins) > MaxAllowedOrigins {
		return nil, fmt.Errorf("%w: at most %d allowed origins", ErrInvalid, MaxAllowedOrigins)
	}
	out := make([]string, 0, len(origins))
	for _, o := range origins {
		n, err := normalizeOrigin(o)
		if err != nil {
			return nil, err
		}
		if !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	return out, nil
}

func normalizeOrigin(raw string) (string, error) {
	o := strings.ToLower(strings.TrimSpace(raw))
	bad := fmt.Errorf("%w: %q is not an origin like https://shop.example.com, https://*.example.com, app://name, file:// or null", ErrInvalid, raw)
	switch {
	case o == OriginNull || o == OriginFile:
		return o, nil
	case o == "" || len(o) > 255 || strings.Contains(o, "*") && !strings.Contains(o, "://*."):
		return "", bad
	}
	scheme, rest, ok := strings.Cut(o, "://")
	if !ok || !schemeRe.MatchString(scheme) || scheme == "file" || scheme == "ws" || scheme == "wss" ||
		strings.ContainsAny(rest, "/?#@\\") {
		return "", bad
	}
	host, port, err := splitHostPort(rest)
	if err != nil {
		return "", bad
	}
	wildcard := strings.HasPrefix(host, "*.")
	if wildcard {
		host = strings.TrimPrefix(host, "*.")
		// *.com would let anybody in: the suffix needs two labels.
		if !strings.Contains(host, ".") || scheme != "http" && scheme != "https" {
			return "", bad
		}
	}
	if !validHost(host) {
		return "", bad
	}
	if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		port = ""
	}
	if wildcard {
		host = "*." + host
	}
	if port != "" {
		host = net.JoinHostPort(host, port)
	}
	return scheme + "://" + host, nil
}

// splitHostPort accepts host, host:port and [v6]:port.
func splitHostPort(s string) (host, port string, err error) {
	if !strings.Contains(s, ":") || (strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]")) {
		return strings.Trim(s, "[]"), "", nil
	}
	host, port, err = net.SplitHostPort(s)
	if err != nil {
		return "", "", err
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port {
		return "", "", errors.New("port must be 1 to 65535")
	}
	return host, port, nil
}

func validHost(h string) bool {
	if net.ParseIP(h) != nil {
		return true
	}
	if h == "" || len(h) > 253 {
		return false
	}
	for _, l := range strings.Split(h, ".") {
		if !labelRe.MatchString(l) {
			return false
		}
	}
	return true
}

// OriginAllowed reports whether the Origin header of a request matches one
// of the allowed origins (as NormalizeOrigins returns them).
func OriginAllowed(allowed []string, origin string) bool {
	origin = strings.ToLower(strings.TrimSpace(origin))
	if origin == "" {
		return false
	}
	if origin == OriginNull || origin == OriginFile {
		return slices.Contains(allowed, origin)
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.User != nil {
		return false
	}
	host, port := u.Hostname(), u.Port()
	if (u.Scheme == "https" && port == "443") || (u.Scheme == "http" && port == "80") {
		port = ""
	}
	canonical := u.Scheme + "://" + host
	if port != "" {
		canonical = u.Scheme + "://" + net.JoinHostPort(host, port)
	}
	for _, a := range allowed {
		if a == canonical {
			return true
		}
		rest, ok := strings.CutPrefix(a, u.Scheme+"://*.")
		if !ok {
			continue
		}
		suffix, wantPort, _ := strings.Cut(rest, ":")
		if wantPort == port && strings.HasSuffix(host, "."+suffix) {
			return true
		}
	}
	return false
}
