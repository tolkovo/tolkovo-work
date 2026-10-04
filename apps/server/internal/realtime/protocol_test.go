package realtime

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/tolkovo/tolkovo/apps/server/internal/platform/authz"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/outbox"
	"github.com/tolkovo/tolkovo/apps/server/internal/realtime/schematest"
)

// samplePayloads are the outbox payloads the producers write today
// (internal/conversations); the integration test in internal/api checks
// the real ones end to end.
var samplePayloads = map[string]string{
	"conversation.created":        `{"conversation_id":"%c","contact_id":"%k","number":7,"status":"queued"}`,
	"conversation.status_changed": `{"conversation_id":"%c","from":"queued","to":"assigned","assignee_id":"%u","actor_id":"%u"}`,
	"conversation.assigned":       `{"conversation_id":"%c","assignee_id":"%u","previous_assignee_id":null,"actor_id":"%u"}`,
	"conversation.tags_changed":   `{"conversation_id":"%c"}`,
	"conversation.read":           `{"conversation_id":"%c","reader_type":"operator","reader_id":"%u","up_to_message_id":"%m"}`,
	"message.created":             `{"conversation_id":"%c","message_id":"%m","kind":"text","is_private":false,"sender_type":"operator","sender_id":"%u"}`,
	"contact.created":             `{"contact_id":"%k"}`,
	"contact.updated":             `{"contact_id":"%k"}`,
}

func samplePayload(topic string) []byte {
	return []byte(strings.NewReplacer(
		"%c", uuid.NewString(), "%k", uuid.NewString(), "%u", uuid.NewString(), "%m", uuid.NewString(),
	).Replace(samplePayloads[topic]))
}

func TestSchema_ListsEveryCloseCodeAndMessageType(t *testing.T) {
	s := schematest.Load(t, schematest.ServerMessage)
	var schemaCodes []websocket.StatusCode
	for _, c := range s.Def("closeCode")["enum"].([]any) {
		n, _ := c.(json.Number).Int64()
		schemaCodes = append(schemaCodes, websocket.StatusCode(n))
	}
	slices.Sort(schemaCodes)
	code := slices.Clone(CloseCodes)
	slices.Sort(code)
	if !slices.Equal(schemaCodes, code) {
		t.Errorf("close codes: schema %v, code %v", schemaCodes, code)
	}

	var schemaTypes []string
	for _, v := range s.Def("messageType")["enum"].([]any) {
		schemaTypes = append(schemaTypes, v.(string))
	}
	codeTypes := []string{TypeHello, TypeHeartbeat, TypeResumed, TypeResyncRequired, TypeConversationTyping, TypeConversationViewers}
	for topic := range samplePayloads {
		codeTypes = append(codeTypes, topic)
	}
	slices.Sort(schemaTypes)
	slices.Sort(codeTypes)
	if !slices.Equal(schemaTypes, codeTypes) {
		t.Errorf("message types: schema %v, code %v", schemaTypes, codeTypes)
	}
}

func TestEncode_EveryMessageMatchesSchema(t *testing.T) {
	s := schematest.Load(t, schematest.ServerMessage)
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	for topic := range samplePayloads {
		if _, ok := channelFor(topic); !ok {
			t.Errorf("%s has a schema but no route: clients would never get it", topic)
		}
		msg, err := encodeEvent(outbox.Event{
			ID: 42, Seq: 1001, WorkspaceID: uuid.New(), Topic: topic, Payload: samplePayload(topic), CreatedAt: now,
		})
		if err != nil {
			t.Fatalf("encode %s: %v", topic, err)
		}
		if err := s.Validate(msg); err != nil {
			t.Errorf("%s: %v\n%s", topic, err, msg)
		}
	}
	for _, msg := range [][]byte{
		encodeControl(TypeHello, helloData{Seq: 5, HeartbeatIntervalMS: 25000}, now),
		encodeControl(TypeHeartbeat, struct{}{}, now),
		encodeControl(TypeResumed, seqData{Seq: 5}, now),
		encodeControl(TypeResyncRequired, seqData{Seq: 9}, now),
		encodeControl(TypeConversationTyping, typingData{ConversationID: uuid.New(), Actor: "operator", ActorID: ptr(uuid.New()), State: StateStart}, now),
		viewers(uuid.New(), now, `[]`),
		viewers(uuid.New(), now, `["`+uuid.NewString()+`","`+uuid.NewString()+`"]`),
	} {
		if err := s.Validate(msg); err != nil {
			t.Errorf("%v\n%s", err, msg)
		}
	}
}

// The validator must reject what the contract forbids, or the test above
// proves nothing.
func TestSchema_RejectsMessagesThatBreakTheContract(t *testing.T) {
	s := schematest.Load(t, schematest.ServerMessage)
	for name, msg := range map[string]string{
		"unknown type":     `{"type":"conversation.exploded","id":"1","ts":"2026-10-04T12:00:00Z","seq":1,"data":{}}`,
		"event no seq":     `{"type":"contact.created","id":"1","ts":"2026-10-04T12:00:00Z","data":{"contact_id":"0b6a1f9e-3c2d-4e5f-8a9b-0c1d2e3f4a5b"}}`,
		"bad uuid":         `{"type":"contact.created","id":"1","ts":"2026-10-04T12:00:00Z","seq":1,"data":{"contact_id":"nope"}}`,
		"bad ts":           `{"type":"heartbeat","id":"1","ts":"yesterday","data":{}}`,
		"negative seq":     `{"type":"resumed","id":"1","ts":"2026-10-04T12:00:00Z","data":{"seq":-1}}`,
		"fractional seq":   `{"type":"resumed","id":"1","ts":"2026-10-04T12:00:00Z","data":{"seq":1.5}}`,
		"missing hb field": `{"type":"hello","id":"1","ts":"2026-10-04T12:00:00Z","data":{"seq":1}}`,
		"viewers dup id":   `{"type":"conversation.viewers","id":"1","ts":"2026-10-04T12:00:00Z","data":{"conversation_id":"0b6a1f9e-3c2d-4e5f-8a9b-0c1d2e3f4a5b","user_ids":["0b6a1f9e-3c2d-4e5f-8a9b-0c1d2e3f4a5b","0b6a1f9e-3c2d-4e5f-8a9b-0c1d2e3f4a5b"]}}`,
		"viewers bad id":   `{"type":"conversation.viewers","id":"1","ts":"2026-10-04T12:00:00Z","data":{"conversation_id":"0b6a1f9e-3c2d-4e5f-8a9b-0c1d2e3f4a5b","user_ids":["nope"]}}`,
		"typing no actor":  `{"type":"conversation.typing","id":"1","ts":"2026-10-04T12:00:00Z","data":{"conversation_id":"0b6a1f9e-3c2d-4e5f-8a9b-0c1d2e3f4a5b","actor_id":null,"state":"start"}}`,
		"status not enum":  `{"type":"conversation.created","id":"1","ts":"2026-10-04T12:00:00Z","seq":1,"data":{"conversation_id":"0b6a1f9e-3c2d-4e5f-8a9b-0c1d2e3f4a5b","contact_id":"0b6a1f9e-3c2d-4e5f-8a9b-0c1d2e3f4a5b","number":1,"status":"lost"}}`,
	} {
		if err := s.Validate([]byte(msg)); err == nil {
			t.Errorf("%s: must be rejected", name)
		}
	}
}

func ptr[T any](v T) *T { return &v }

// viewers assembles conversation.viewers as the Redis script does.
func viewers(conversationID uuid.UUID, now time.Time, ids string) []byte {
	head, tail := viewersMessage(conversationID, now)
	return []byte(string(head) + ids + string(tail))
}

func TestParseClientMessage(t *testing.T) {
	client := schematest.Load(t, schematest.ClientMessage)
	conv := uuid.New()
	valid := map[string]clientMessage{
		`{"type":"resume","id":"r1","ts":"2026-10-04T12:00:00.123Z","data":{"last_seq":17}}`:                                         {Type: TypeResume, LastSeq: 17},
		`{"type":"typing","id":"t1","ts":"2026-10-04T12:00:00Z","data":{"conversation_id":"` + conv.String() + `","state":"start"}}`: {Type: TypeTyping, ConversationID: conv, Start: true},
		`{"type":"viewing","id":"v1","ts":"2026-10-04T12:00:00Z","data":{"conversation_id":"` + conv.String() + `","state":"stop"}}`: {Type: TypeViewing, ConversationID: conv},
	}
	for msg, want := range valid {
		if err := client.Validate([]byte(msg)); err != nil {
			t.Fatalf("the sample must match the client schema: %v\n%s", err, msg)
		}
		if got, err := parseClientMessage([]byte(msg)); err != nil || got != want {
			t.Fatalf("%s: got %+v, %v", msg, got, err)
		}
	}
	c := conv.String()
	for name, msg := range map[string]string{
		"not json":        `resume 17`,
		"other type":      `{"type":"subscribe","id":"r1","ts":"2026-10-04T12:00:00Z","data":{"last_seq":1}}`,
		"no id":           `{"type":"resume","ts":"2026-10-04T12:00:00Z","data":{"last_seq":1}}`,
		"long id":         `{"type":"resume","id":"` + strings.Repeat("x", 65) + `","ts":"2026-10-04T12:00:00Z","data":{"last_seq":1}}`,
		"no last_seq":     `{"type":"resume","id":"r1","ts":"2026-10-04T12:00:00Z","data":{}}`,
		"negative":        `{"type":"resume","id":"r1","ts":"2026-10-04T12:00:00Z","data":{"last_seq":-1}}`,
		"string seq":      `{"type":"resume","id":"r1","ts":"2026-10-04T12:00:00Z","data":{"last_seq":"1"}}`,
		"bad timestamp":   `{"type":"resume","id":"r1","ts":"now","data":{"last_seq":1}}`,
		"typing no conv":  `{"type":"typing","id":"t1","ts":"2026-10-04T12:00:00Z","data":{"state":"start"}}`,
		"typing bad conv": `{"type":"typing","id":"t1","ts":"2026-10-04T12:00:00Z","data":{"conversation_id":"42","state":"start"}}`,
		"upper-case conv": `{"type":"typing","id":"t1","ts":"2026-10-04T12:00:00Z","data":{"conversation_id":"` + strings.ToUpper(c) + `","state":"start"}}`,
		"typing no state": `{"type":"typing","id":"t1","ts":"2026-10-04T12:00:00Z","data":{"conversation_id":"` + c + `"}}`,
		"viewing state":   `{"type":"viewing","id":"v1","ts":"2026-10-04T12:00:00Z","data":{"conversation_id":"` + c + `","state":"maybe"}}`,
	} {
		if _, err := parseClientMessage([]byte(msg)); err == nil {
			t.Errorf("%s: must be rejected", name)
		}
		if client.Validate([]byte(msg)) == nil {
			t.Errorf("%s: the client schema must reject it too", name)
		}
	}
}

func TestAllowMessage_TokenBucket(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	g := &Gateway{o: Options{Now: func() time.Time { return now }}.withDefaults()}
	c := newConn(g, nil, Client{UserID: uuid.New(), WorkspaceID: uuid.New(), Role: authz.RoleOperator}, func() {})
	for i := range ClientMessageBurst {
		if !c.allowMessage() {
			t.Fatalf("message %d of the burst refused", i+1)
		}
	}
	if c.allowMessage() {
		t.Fatal("the burst is spent")
	}
	now = now.Add(time.Second)
	for i := range ClientMessageRate {
		if !c.allowMessage() {
			t.Fatalf("refill message %d refused", i+1)
		}
	}
	if c.allowMessage() {
		t.Fatal("refill is ClientMessageRate per second")
	}
	now = now.Add(time.Hour)
	for range ClientMessageBurst {
		_ = c.allowMessage()
	}
	if c.allowMessage() {
		t.Fatal("a long pause refills up to the burst, not beyond")
	}
}

func TestChannelNames(t *testing.T) {
	ws := uuid.New()
	name := redisChannel("t1:", ws, "conversations")
	if name != "t1:ws:"+ws.String()+":conversations" {
		t.Fatalf("channel name: %s", name)
	}
	got, ch, ok := parseChannel("t1:", name)
	if !ok || got != ws || ch != "conversations" {
		t.Fatalf("parse: %v %s %v", got, ch, ok)
	}
	for _, bad := range []string{"ws:" + ws.String() + ":x", "t1:ws:nope:x", "t1:ws:" + ws.String(), "t1:ws:" + ws.String() + ":"} {
		if _, _, ok := parseChannel("t1:", bad); ok {
			t.Errorf("%q must not parse", bad)
		}
	}
}

func TestRoutes_FollowPermissions(t *testing.T) {
	if _, ok := channelFor("test.thing.created"); ok {
		t.Error("topics without a route must stay on the server")
	}
	if got := topicPatterns(authz.RoleOperator); !slices.Equal(got, []string{"conversation.%", "message.%", "contact.%"}) {
		t.Errorf("operator patterns: %v", got)
	}
	if got := topicPatterns("nobody"); len(got) != 0 {
		t.Errorf("an unknown role sees nothing, got %v", got)
	}
	if channelAllowed("nobody", "conversations") || !channelAllowed(authz.RoleOperator, "contacts") {
		t.Error("channelAllowed must follow the RBAC matrix")
	}
}

func TestDeliverLive_ClosesSlowConsumerAndSkipsForbiddenChannels(t *testing.T) {
	g := &Gateway{o: Options{SendBuffer: 1}.withDefaults()}
	c := newConn(g, nil, Client{UserID: uuid.New(), WorkspaceID: uuid.New(), Role: authz.RoleOperator}, func() {})
	c.deliverLive("conversations", []byte(`{"seq":3}`), 3)
	if c.failed() || c.lastSeq.Load() != 3 {
		t.Fatalf("first event must be queued, failed=%v seq=%d", c.failed(), c.lastSeq.Load())
	}
	c.deliverLive("conversations", []byte(`{"seq":4}`), 4)
	if !c.failed() || c.closeCode() != CloseSlowConsumer {
		t.Fatalf("overflow must close with 4408, got %d", c.closeCode())
	}

	blind := newConn(g, nil, Client{UserID: uuid.New(), WorkspaceID: uuid.New(), Role: "nobody"}, func() {})
	blind.deliverLive("conversations", []byte(`{"seq":5}`), 5)
	if len(blind.live) != 0 {
		t.Fatal("a role without conversations.view must not get conversation events")
	}
}
