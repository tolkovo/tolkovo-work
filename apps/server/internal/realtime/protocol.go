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
	TypeResume         = "resume" // the only client → server message
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

// clientMessage is a decoded client frame; see client-message.schema.json.
type clientMessage struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	TS   string `json:"ts"`
	Data struct {
		LastSeq *int64 `json:"last_seq"`
	} `json:"data"`
}

// parseResume validates a resume frame and returns its last_seq.
func parseResume(b []byte) (int64, error) {
	var m clientMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return 0, fmt.Errorf("not a JSON object: %w", err)
	}
	switch {
	case m.Type != TypeResume:
		return 0, fmt.Errorf("unsupported message type %q", m.Type)
	case m.ID == "" || len(m.ID) > 64:
		return 0, fmt.Errorf("id must be 1–64 characters")
	case m.Data.LastSeq == nil || *m.Data.LastSeq < 0:
		return 0, fmt.Errorf("data.last_seq must be a non-negative integer")
	}
	if _, err := time.Parse(time.RFC3339Nano, m.TS); err != nil {
		return 0, fmt.Errorf("ts must be an RFC 3339 time")
	}
	return *m.Data.LastSeq, nil
}

// eventSeq reads seq from an encoded event without decoding data.
func eventSeq(msg []byte) int64 {
	var v struct {
		Seq int64 `json:"seq"`
	}
	_ = json.Unmarshal(msg, &v)
	return v.Seq
}
