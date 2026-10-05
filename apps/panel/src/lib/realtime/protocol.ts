import { z } from "zod";

/**
 * The realtime protocol of api/ws/server-message.schema.json (the source of truth). There is
 * no generator for WS types yet (packages/ws-protocol, docs/00 §4.1), so this file mirrors the
 * envelope by hand and protocol.test.ts checks it against the schema: types, close codes.
 * Messages are parsed with zod at the boundary (docs/00 §7.1).
 */

export const REALTIME_PATH = "/api/v1/realtime";

/** WebSocket close codes the gateway uses ($defs/closeCode). */
export const CLOSE_CODES = {
  protocolViolation: 1008,
  messageTooBig: 1009,
  internalError: 1011,
  serviceRestart: 1012,
  sessionEnded: 4401,
  slowConsumer: 4408,
  tooManyConnections: 4429,
} as const;

export const CONTROL_TYPES = ["hello", "heartbeat", "resumed", "resync_required"] as const;

/** Ephemeral signals: no seq, never replayed, not deduplicated (docs/02 §10.5). */
export const SIGNAL_TYPES = ["conversation.typing", "conversation.viewers"] as const;

/** Domain events the server sends today ($defs/messageType minus control messages). */
export const EVENT_TYPES = [
  "conversation.created",
  "conversation.status_changed",
  "conversation.assigned",
  "conversation.tags_changed",
  "conversation.issues_changed",
  "conversation.read",
  "message.created",
  "message.delivery",
  "contact.created",
  "contact.updated",
] as const;

export type EventType = (typeof EVENT_TYPES)[number];

const seq = z.number().int().nonnegative();
const envelope = z.object({
  id: z.string().min(1).max(64),
  ts: z.string(),
});
const seqData = z.object({ seq });

const hello = envelope.extend({
  type: z.literal("hello"),
  data: z.object({ seq, heartbeat_interval_ms: z.number().int().min(1000) }),
});
const heartbeat = envelope.extend({ type: z.literal("heartbeat"), data: z.object({}) });
const resumed = envelope.extend({ type: z.literal("resumed"), data: seqData });
const resyncRequired = envelope.extend({ type: z.literal("resync_required"), data: seqData });

const typing = envelope.extend({
  type: z.literal("conversation.typing"),
  data: z.object({
    conversation_id: z.string(),
    actor: z.enum(["operator", "contact", "bot"]),
    actor_id: z.string().nullable(),
    state: z.enum(["start", "stop"]),
  }),
});
const viewers = envelope.extend({
  type: z.literal("conversation.viewers"),
  data: z.object({ conversation_id: z.string(), user_ids: z.array(z.string()) }),
});

/**
 * A domain event. Types the panel does not know yet are still events: a new event type is not
 * a breaking change (docs/00 §8), so they advance the cursor and are otherwise ignored.
 */
const event = envelope.extend({
  type: z.string().regex(/^[a-z][a-z_]*(\.[a-z][a-z_]*)+$/),
  seq,
  data: z.record(z.string(), z.unknown()),
});

export const serverMessageSchema = z.union([hello, heartbeat, resumed, resyncRequired, typing, viewers, event]);

export type ServerMessage = z.infer<typeof serverMessageSchema>;
export type RealtimeEvent = z.infer<typeof event>;
export type RealtimeSignal = z.infer<typeof typing> | z.infer<typeof viewers>;

/** Parses one text frame; undefined for anything outside the contract. */
export function parseServerMessage(raw: unknown): ServerMessage | undefined {
  if (typeof raw !== "string") return undefined;
  let value: unknown;
  try {
    value = JSON.parse(raw);
  } catch {
    return undefined;
  }
  const parsed = serverMessageSchema.safeParse(value);
  return parsed.success ? parsed.data : undefined;
}

export function isSignal(message: ServerMessage): message is RealtimeSignal {
  return (SIGNAL_TYPES as readonly string[]).includes(message.type);
}

export function isEvent(message: ServerMessage): message is RealtimeEvent {
  return "seq" in message && !(CONTROL_TYPES as readonly string[]).includes(message.type);
}

/** Client messages (client-message.schema.json). */
export function resumeMessage(lastSeq: number, id: string, now: Date): string {
  return JSON.stringify({ type: "resume", id, ts: now.toISOString(), data: { last_seq: lastSeq } });
}

/**
 * typing or viewing of one conversation: start (repeated while it lasts) or stop. A typing start
 * says whether it is a reply: only that one reaches the customer's Telegram as "typing…".
 */
export function signalMessage(
  type: "typing" | "viewing",
  conversationId: string,
  state: "start" | "stop",
  id: string,
  now: Date,
  mode?: "reply" | "note",
): string {
  const data = mode ? { conversation_id: conversationId, state, mode } : { conversation_id: conversationId, state };
  return JSON.stringify({ type, id, ts: now.toISOString(), data });
}

/** ws(s)://host/api/v1/realtime for a page served from origin. */
export function realtimeUrl(origin: string): string {
  const url = new URL(REALTIME_PATH, origin);
  url.protocol = url.protocol === "https:" ? "wss:" : "ws:";
  return url.href;
}
