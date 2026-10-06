package realtime

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/tolkovo/tolkovo/apps/server/internal/platform/authz"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/outbox"
)

// Message types of api/ws/*.schema.json. Control messages have no dot;
// domain events keep their outbox topic as the type.
const (
	TypeHello          = "hello"
	TypeHeartbeat      = "heartbeat"
	TypeResumed        = "resumed"
	TypeResyncRequired = "resync_required"

	// Signals: ephemeral, relayed through Redis without the outbox, so
	// they carry no seq and are never replayed (docs/02 §10.5).
	TypeConversationTyping  = "conversation.typing"
	TypeConversationViewers = "conversation.viewers"

	// Client → server messages.
	TypeResume  = "resume"
	TypeTyping  = "typing"
	TypeViewing = "viewing"
)

// Typing modes: a reply reaches the customer's channel, a note does not.
const (
	ModeReply = "reply"
	ModeNote  = "note"
)

// Signal states of typing and viewing.
const (
	StateStart = "start"
	StateStop  = "stop"
)

// Close codes, $defs/closeCode in api/ws/server-message.schema.json.
const (
	CloseProtocolViolation  = websocket.StatusPolicyViolation // 1008: unknown or repeated client message
	CloseMessageTooBig      = websocket.StatusMessageTooBig   // 1009: sent by the library on the read limit
	CloseInternalError      = websocket.StatusInternalError   // 1011: reconnect with backoff
	CloseServiceRestart     = websocket.StatusServiceRestart  // 1012: shutdown, reconnect with jitter
	CloseSessionEnded       = websocket.StatusCode(4401)      // signed out, revoked, expired: do not reconnect
	CloseSlowConsumer       = websocket.StatusCode(4408)      // send buffer overflow: reconnect and resume
	CloseTooManyConnections = websocket.StatusCode(4429)      // per-user limit: long backoff
)

// CloseCodes lists every code above; a test compares it with the schema.
var CloseCodes = []websocket.StatusCode{
	CloseProtocolViolation, CloseMessageTooBig, CloseInternalError, CloseServiceRestart,
	CloseSessionEnded, CloseSlowConsumer, CloseTooManyConnections,
}

// MaxClientMessageBytes bounds a client frame (resume is ~100 bytes).
const MaxClientMessageBytes = 1024

// Client message rate: a token bucket of ClientMessageBurst refilled at
// ClientMessageRate per second. The panel sends typing at most every 3 s
// and refreshes viewing every 20 s, so only a broken client hits it.
const (
	ClientMessageBurst = 20
	ClientMessageRate  = 2
)

// ViewerTTL is how long a viewer stays listed after its last viewing
// start; clients refresh every 20 s.
const ViewerTTL = 45 * time.Second

// route maps outbox topics to a fan-out channel and the permission needed
// to receive it. Topics without a route never leave the server: a new
// event type reaches clients only after it is added here and to the
// schema (contract first, docs/00 §8).
type route struct {
	prefix  string // outbox topic prefix, e.g. "conversation."
	channel string // {topic} of the Redis channel ws:{workspace}:{topic}
	perm    authz.Permission
}

// Phase 1 has no line, team or channel scopes (docs/04 §4.3): a role that
// may view conversations sees every conversation event of its workspace.
// Scoped topics (team:, user:, conv:) arrive with them (docs/02 §10.3).
var routes = []route{
	{prefix: "conversation.", channel: "conversations", perm: authz.ConversationsView},
	{prefix: "message.", channel: "conversations", perm: authz.ConversationsView},
	{prefix: "contact.", channel: "contacts", perm: authz.ContactsView},
	{prefix: "task.", channel: "tasks", perm: authz.TasksView},
}

// channelFor returns the fan-out channel of an outbox topic.
func channelFor(topic string) (string, bool) {
	for _, r := range routes {
		if strings.HasPrefix(topic, r.prefix) {
			return r.channel, true
		}
	}
	return "", false
}

// channelAllowed reports whether role may receive events of channel.
func channelAllowed(role, channel string) bool {
	for _, r := range routes {
		if r.channel == channel && authz.Allowed(role, r.perm) {
			return true
		}
	}
	return false
}

// topicPatterns are the LIKE patterns of topics role may replay.
func topicPatterns(role string) []string {
	var out []string
	for _, r := range routes {
		if authz.Allowed(role, r.perm) {
			out = append(out, r.prefix+"%")
		}
	}
	return out
}

// redisChannel is ws:{workspace}:{topic}, behind an optional test prefix.
func redisChannel(prefix string, workspaceID uuid.UUID, channel string) string {
	return prefix + "ws:" + workspaceID.String() + ":" + channel
}

// parseChannel is the inverse of redisChannel.
func parseChannel(prefix, name string) (uuid.UUID, string, bool) {
	rest, ok := strings.CutPrefix(name, prefix+"ws:")
	if !ok {
		return uuid.Nil, "", false
	}
	ws, channel, ok := strings.Cut(rest, ":")
	if !ok || channel == "" {
		return uuid.Nil, "", false
	}
	id, err := uuid.Parse(ws)
	if err != nil {
		return uuid.Nil, "", false
	}
	return id, channel, true
}

// envelope is the wire format {type, id, ts, seq?, data}.
type envelope struct {
	Type string    `json:"type"`
	ID   string    `json:"id"`
	TS   time.Time `json:"ts"`
	Seq  *int64    `json:"seq,omitempty"`
	Data any       `json:"data"`
}

// encodeEvent turns a published outbox event into its wire message. The
// payload passes through untouched: producers keep it to ids (outbox.Add).
func encodeEvent(e outbox.Event) ([]byte, error) {
	data := e.Payload
	if len(data) == 0 {
		data = json.RawMessage(`{}`)
	}
	seq := e.Seq
	b, err := json.Marshal(envelope{
		Type: e.Topic, ID: fmt.Sprint(e.ID), TS: e.CreatedAt.UTC(), Seq: &seq, Data: data,
	})
	if err != nil {
		return nil, fmt.Errorf("realtime: encode event %d: %w", e.ID, err)
	}
	return b, nil
}

// encodeControl builds a control message with a fresh id.
func encodeControl(typ string, data any, now time.Time) []byte {
	// Control payloads are fixed structs of numbers: marshalling cannot fail.
	b, _ := json.Marshal(envelope{Type: typ, ID: uuid.NewString(), TS: now.UTC(), Data: data})
	return b
}

type helloData struct {
	Seq                 int64 `json:"seq"`
	HeartbeatIntervalMS int64 `json:"heartbeat_interval_ms"`
}

type seqData struct {
	Seq int64 `json:"seq"`
}

// clientMessage is a validated client frame; see client-message.schema.json.
type clientMessage struct {
	Type           string
	LastSeq        int64     // resume
	ConversationID uuid.UUID // typing, viewing
	Start          bool      // typing, viewing: start rather than stop
	Reply          bool      // typing: of a reply the customer will get, not a note
}

type rawClientMessage struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	TS   string `json:"ts"`
	Data struct {
		LastSeq        *int64 `json:"last_seq"`
		ConversationID string `json:"conversation_id"`
		State          string `json:"state"`
		Mode           string `json:"mode"`
	} `json:"data"`
}

// parseClientMessage validates a client frame.
func parseClientMessage(b []byte) (clientMessage, error) {
	var m rawClientMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return clientMessage{}, fmt.Errorf("not a JSON object: %w", err)
	}
	if m.ID == "" || len(m.ID) > 64 {
		return clientMessage{}, fmt.Errorf("id must be 1–64 characters")
	}
	if _, err := time.Parse(time.RFC3339Nano, m.TS); err != nil {
		return clientMessage{}, fmt.Errorf("ts must be an RFC 3339 time")
	}
	out := clientMessage{Type: m.Type}
	switch m.Type {
	case TypeResume:
		if m.Data.LastSeq == nil || *m.Data.LastSeq < 0 {
			return clientMessage{}, fmt.Errorf("data.last_seq must be a non-negative integer")
		}
		out.LastSeq = *m.Data.LastSeq
	case TypeTyping, TypeViewing:
		id, err := parseUUID(m.Data.ConversationID)
		if err != nil {
			return clientMessage{}, fmt.Errorf("data.conversation_id must be a UUID")
		}
		if m.Data.State != StateStart && m.Data.State != StateStop {
			return clientMessage{}, fmt.Errorf("data.state must be start or stop")
		}
		out.ConversationID, out.Start = id, m.Data.State == StateStart
		if m.Type == TypeTyping {
			if m.Data.Mode != "" && m.Data.Mode != ModeReply && m.Data.Mode != ModeNote {
				return clientMessage{}, fmt.Errorf("data.mode must be reply or note")
			}
			out.Reply = m.Data.Mode == ModeReply
		}
	default:
		return clientMessage{}, fmt.Errorf("unsupported message type %q", m.Type)
	}
	return out, nil
}

// parseUUID accepts only the canonical lower-case form of the schema.
func parseUUID(s string) (uuid.UUID, error) {
	id, err := uuid.Parse(s)
	if err != nil || id.String() != s {
		return uuid.Nil, fmt.Errorf("not a canonical UUID")
	}
	return id, nil
}

type typingData struct {
	ConversationID uuid.UUID  `json:"conversation_id"`
	Actor          string     `json:"actor"`
	ActorID        *uuid.UUID `json:"actor_id"`
	State          string     `json:"state"`
}

// eventSeq reads seq from an encoded event without decoding data.
func eventSeq(msg []byte) int64 {
	var v struct {
		Seq int64 `json:"seq"`
	}
	_ = json.Unmarshal(msg, &v)
	return v.Seq
}
