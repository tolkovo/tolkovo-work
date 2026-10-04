package realtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// signals relays ephemeral signals (docs/02 §10.5): typing and who has a
// conversation open. They go straight to the fan-out channel of the
// workspace, bypassing the outbox: losing one costs a moment of a stale
// indicator, never data. Payloads are ids only, like domain events.
type signals struct {
	rdb    *redis.Client
	prefix string
	now    func() time.Time
}

// typing relays conversation.typing of an operator.
func (s signals) typing(ctx context.Context, workspaceID, userID, conversationID uuid.UUID, start bool) error {
	msg := encodeControl(TypeConversationTyping, typingData{
		ConversationID: conversationID, Actor: "operator", ActorID: &userID, State: state(start),
	}, s.now())
	if err := s.rdb.Publish(ctx, redisChannel(s.prefix, workspaceID, "conversations"), msg).Err(); err != nil {
		return fmt.Errorf("realtime: publish typing: %w", err)
	}
	return nil
}

// viewersScript keeps the viewers of one conversation in a sorted set of
// "{user}/{connection}" members scored by expiry (ms), so several tabs of
// one user count once and a crashed replica's viewers expire on their own.
// It applies one change, drops expired members and publishes the whole
// list in the same step, so subscribers never see lists out of order.
//
// KEYS[1] the set, KEYS[2] the fan-out channel.
// ARGV: now ms, expiry ms, member, "add" or "rem", message head, message
// tail, key TTL ms. The message is head .. JSON array of user ids .. tail.
var viewersScript = redis.NewScript(`
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', ARGV[1])
if ARGV[4] == 'add' then
  redis.call('ZADD', KEYS[1], ARGV[2], ARGV[3])
else
  redis.call('ZREM', KEYS[1], ARGV[3])
end
local seen, ids = {}, {}
for _, m in ipairs(redis.call('ZRANGE', KEYS[1], 0, -1)) do
  local u = string.match(m, '^([0-9a-f-]+)/')
  if u and not seen[u] then
    seen[u] = true
    ids[#ids + 1] = u
  end
end
if #ids == 0 then
  redis.call('DEL', KEYS[1])
else
  redis.call('PEXPIRE', KEYS[1], ARGV[7])
end
table.sort(ids)
local list = '[]'
if #ids > 0 then list = '["' .. table.concat(ids, '","') .. '"]' end
redis.call('PUBLISH', KEYS[2], ARGV[5] .. list .. ARGV[6])
return #ids
`)

// idsPlaceholder marks where the script puts the list of user ids.
const idsPlaceholder = "\x00ids\x00"

// viewing adds (start) or removes the connection as a viewer of the
// conversation and publishes conversation.viewers.
func (s signals) viewing(ctx context.Context, workspaceID uuid.UUID, conversationID uuid.UUID, member string, start bool) error {
	now := s.now()
	head, tail := viewersMessage(conversationID, now)
	op := "rem"
	if start {
		op = "add"
	}
	keys := []string{
		s.prefix + "viewers:" + workspaceID.String() + ":" + conversationID.String(),
		redisChannel(s.prefix, workspaceID, "conversations"),
	}
	err := viewersScript.Run(ctx, s.rdb, keys,
		now.UnixMilli(), now.Add(ViewerTTL).UnixMilli(), member, op, head, tail, (ViewerTTL + 15*time.Second).Milliseconds(),
	).Err()
	if err != nil {
		return fmt.Errorf("realtime: update viewers: %w", err)
	}
	return nil
}

// viewersMessage is conversation.viewers split where the list of user ids
// goes; the script joins head, the JSON array and tail.
func viewersMessage(conversationID uuid.UUID, now time.Time) (head, tail []byte) {
	// A fixed struct of a UUID, strings and a time: marshalling cannot fail.
	msg, _ := json.Marshal(envelope{
		Type: TypeConversationViewers, ID: uuid.NewString(), TS: now.UTC(),
		Data: map[string]any{"conversation_id": conversationID, "user_ids": idsPlaceholder},
	})
	placeholder, _ := json.Marshal(idsPlaceholder)
	head, tail, _ = bytes.Cut(msg, placeholder)
	return head, tail
}

func state(start bool) string {
	if start {
		return StateStart
	}
	return StateStop
}
