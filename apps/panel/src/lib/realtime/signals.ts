import { useEffect, useMemo } from "react";
import { create } from "zustand";

import { signalMessage, type RealtimeSignal } from "./protocol";

/**
 * Ephemeral signals of the realtime protocol (docs/02 §10.5): who is typing and who has a
 * conversation open. They are not server state, so they live in a small store, not in the
 * Query cache, and expire on their own: a lost "stop" must not leave an indicator forever.
 */

/** A typing indicator lasts this long after its last start (server-message.schema.json). */
export const TYPING_TTL_MS = 6_000;
/** A list of viewers not refreshed for this long is stale. */
export const VIEWERS_TTL_MS = 60_000;
/** The panel repeats viewing start this often; the server forgets a viewer after 45 s. */
export const VIEWING_REFRESH_MS = 20_000;
/** typing start is sent at most this often while the operator types. */
export const TYPING_THROTTLE_MS = 3_000;

export type Typist = { actor: "operator" | "contact" | "bot"; actorId: string | null };

type SignalState = {
  /** Per conversation: other members' ids, as the server listed them. */
  viewers: Record<string, string[]>;
  /** Per conversation: keys of who types ("operator:{id}", "contact:{id}"). */
  typing: Record<string, string[]>;
};

export const useSignalStore = create<SignalState>(() => ({ viewers: {}, typing: {} }));

const typistKey = (actor: string, actorId: string | null) => `${actor}:${actorId ?? ""}`;

function parseTypist(key: string): Typist {
  const [actor, actorId] = key.split(":") as [Typist["actor"], string];
  return { actor, actorId: actorId || null };
}

/**
 * Expiry timers: an entry disappears TTL after its last signal, so hooks stay pure selectors.
 * Keyed by "v:{conversation}" for viewers and "t:{conversation}:{typist}" for typing.
 */
const timers = new Map<string, ReturnType<typeof setTimeout>>();

function expireAfter(key: string, ms: number, drop: () => void): void {
  clearTimeout(timers.get(key));
  timers.set(
    key,
    setTimeout(() => {
      timers.delete(key);
      drop();
    }, ms),
  );
}

function setViewers(conversationId: string, userIds: string[] | undefined): void {
  useSignalStore.setState((s) => {
    const viewers = { ...s.viewers };
    if (userIds) viewers[conversationId] = userIds;
    else delete viewers[conversationId];
    return { viewers };
  });
}

function setTyping(conversationId: string, key: string, on: boolean): void {
  useSignalStore.setState((s) => {
    const current = s.typing[conversationId] ?? [];
    if (on === current.includes(key)) return s;
    const next = on ? [...current, key] : current.filter((k) => k !== key);
    return { typing: { ...s.typing, [conversationId]: next } };
  });
}

export function applySignal(signal: RealtimeSignal): void {
  const id = signal.data.conversation_id;
  if (signal.type === "conversation.viewers") {
    setViewers(id, signal.data.user_ids);
    expireAfter(`v:${id}`, VIEWERS_TTL_MS, () => setViewers(id, undefined));
    return;
  }
  const key = typistKey(signal.data.actor, signal.data.actor_id);
  if (signal.data.state === "start") {
    setTyping(id, key, true);
    expireAfter(`t:${id}:${key}`, TYPING_TTL_MS, () => setTyping(id, key, false));
  } else {
    clearTyping(id, signal.data.actor, signal.data.actor_id);
  }
}

/** A message from someone ends their typing indicator. */
export function clearTyping(conversationId: string, actor: string, actorId: string | null): void {
  const key = typistKey(actor, actorId);
  clearTimeout(timers.get(`t:${conversationId}:${key}`));
  timers.delete(`t:${conversationId}:${key}`);
  setTyping(conversationId, key, false);
}

/** Another session (or none): nothing seen in the previous one may be shown. */
export function resetSignals(): void {
  for (const timer of timers.values()) clearTimeout(timer);
  timers.clear();
  useSignalStore.setState({ viewers: {}, typing: {} });
}

/** Other members who have the conversation open, in the server's order. */
export function useViewers(conversationId: string, meId: string | undefined): string[] {
  const ids = useSignalStore((s) => s.viewers[conversationId]);
  return useMemo(() => (ids ?? []).filter((id) => id !== meId), [ids, meId]);
}

/** Who is typing in the conversation now, except the signed-in operator. */
export function useTypists(conversationId: string, meId: string | undefined): Typist[] {
  const keys = useSignalStore((s) => s.typing[conversationId]);
  return useMemo(
    () => (keys ?? []).filter((key) => key !== typistKey("operator", meId ?? null)).map(parseTypist),
    [keys, meId],
  );
}

type Sink = { send(message: string): boolean };

type SenderOptions = {
  now?: () => Date;
  newId?: () => string;
  setInterval?: (fn: () => void, ms: number) => ReturnType<typeof setInterval>;
  clearInterval?: (timer: ReturnType<typeof setInterval>) => void;
};

/**
 * Sends the operator's own signals over the current connection. It remembers the open
 * conversation, refreshes it every VIEWING_REFRESH_MS and announces it again after a reconnect:
 * the server keeps viewers per connection.
 */
export class SignalSender {
  private sink: Sink | null = null;
  private viewing: string | null = null;
  private refresh: ReturnType<typeof setInterval> | undefined;
  private lastTyping: { conversationId: string; at: number } | null = null;
  private readonly o: Required<SenderOptions>;

  constructor(options: SenderOptions = {}) {
    this.o = {
      now: () => new Date(),
      newId: () => crypto.randomUUID(),
      setInterval: (fn, ms) => setInterval(fn, ms),
      clearInterval: (timer) => clearInterval(timer),
      ...options,
    };
  }

  /** The connection of the current session, or null when there is none. */
  attach(sink: Sink | null): void {
    this.sink = sink;
    this.lastTyping = null;
  }

  /** The connection (re)opened: what still holds is sent again. */
  ready(): void {
    this.lastTyping = null;
    if (this.viewing) this.send("viewing", this.viewing, "start");
  }

  /** The conversation now open in this tab, or null when none is. */
  view(conversationId: string | null): void {
    if (conversationId === this.viewing) return;
    if (this.viewing && !conversationId) this.send("viewing", this.viewing, "stop");
    this.viewing = conversationId;
    if (this.refresh !== undefined) this.o.clearInterval(this.refresh);
    this.refresh = undefined;
    if (!conversationId) return;
    this.send("viewing", conversationId, "start");
    this.refresh = this.o.setInterval(() => {
      if (this.viewing) this.send("viewing", this.viewing, "start");
    }, VIEWING_REFRESH_MS);
  }

  /** Call on every keystroke: start goes out at most every TYPING_THROTTLE_MS. */
  typing(conversationId: string): void {
    const at = this.o.now().getTime();
    const last = this.lastTyping;
    if (last && last.conversationId === conversationId && at - last.at < TYPING_THROTTLE_MS) return;
    if (this.send("typing", conversationId, "start")) this.lastTyping = { conversationId, at };
  }

  /** Sent, cleared or left the composer. */
  stopTyping(conversationId: string): void {
    if (this.lastTyping?.conversationId !== conversationId) return;
    this.lastTyping = null;
    this.send("typing", conversationId, "stop");
  }

  private send(type: "typing" | "viewing", conversationId: string, state: "start" | "stop"): boolean {
    return this.sink?.send(signalMessage(type, conversationId, state, this.o.newId(), this.o.now())) ?? false;
  }
}

/** The page's sender; session-binding.ts attaches the connection to it. */
export const signalSender = new SignalSender();

/** Tells colleagues this conversation is open here while the component is mounted. */
export function useViewingSignal(conversationId: string): void {
  useEffect(() => {
    signalSender.view(conversationId);
    return () => signalSender.view(null);
  }, [conversationId]);
}
