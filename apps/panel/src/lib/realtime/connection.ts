import {
  CLOSE_CODES,
  isEvent,
  parseServerMessage,
  resumeMessage,
  type RealtimeEvent,
  type ServerMessage,
} from "./protocol";

/** The part of the browser WebSocket the connection uses; tests pass a fake. */
export interface WebSocketLike {
  send(data: string): void;
  close(code?: number, reason?: string): void;
  onopen: ((this: WebSocket, ev: Event) => unknown) | null;
  onmessage: ((this: WebSocket, ev: MessageEvent) => unknown) | null;
  onclose: ((this: WebSocket, ev: CloseEvent) => unknown) | null;
  onerror: ((this: WebSocket, ev: Event) => unknown) | null;
}

export type RealtimeHandlers = {
  /** A domain event, once per id. */
  onEvent: (event: RealtimeEvent) => void;
  /** Missed events cannot be replayed (first connection, pruned, too many): refetch over REST. */
  onResync: () => void;
  /** The server closed with 4401: the session is over. No reconnects until restarted. */
  onSessionEnded: () => void;
};

export type ConnectionOptions = {
  url: string;
  handlers: RealtimeHandlers;
  createSocket?: (url: string) => WebSocketLike;
  random?: () => number;
  now?: () => Date;
  newId?: () => string;
  /** First reconnect delay; doubles per failed attempt up to maxDelayMs. */
  baseDelayMs?: number;
  maxDelayMs?: number;
};

/** Heartbeat period assumed until hello says otherwise (the server default, docs/02 §10.1). */
const DEFAULT_HEARTBEAT_MS = 25_000;
/** Remembered event ids for deduplication; replay and live delivery may overlap. */
const SEEN_LIMIT = 1000;

/**
 * One realtime connection of the panel (ADR-0005): reconnects with exponential backoff and
 * jitter, resumes from the last seen seq, deduplicates events by id and watches heartbeats.
 * It knows nothing about React or the query cache; session-binding.ts wires it to them.
 */
export class RealtimeConnection {
  private readonly o: Required<Omit<ConnectionOptions, "handlers" | "url">> & Pick<ConnectionOptions, "url">;
  private readonly handlers: RealtimeHandlers;
  private socket: WebSocketLike | null = null;
  private running = false;
  private attempt = 0;
  private lastSeq: number | null = null;
  /**
   * While a resume is answered, live events may overtake replayed ones. Their seq is held back
   * until resumed: a disconnect mid-replay must resume from before the gap, not after it.
   */
  private resuming = false;
  private pendingSeq = 0;
  private seen = new Set<string>();
  private heartbeatMs = DEFAULT_HEARTBEAT_MS;
  private reconnectTimer: ReturnType<typeof setTimeout> | undefined;
  private watchdog: ReturnType<typeof setTimeout> | undefined;

  constructor({ handlers, ...options }: ConnectionOptions) {
    this.handlers = handlers;
    this.o = {
      createSocket: (url) => new WebSocket(url),
      random: Math.random,
      now: () => new Date(),
      newId: () => crypto.randomUUID(),
      baseDelayMs: 500,
      maxDelayMs: 30_000,
      ...options,
    };
  }

  start(): void {
    if (this.running) return;
    this.running = true;
    this.connect();
  }

  stop(): void {
    this.running = false;
    clearTimeout(this.reconnectTimer);
    clearTimeout(this.watchdog);
    this.detach()?.close(1000, "stop");
  }

  private connect(): void {
    const socket = this.o.createSocket(this.o.url);
    this.socket = socket;
    // Handlers check identity: events of a replaced socket are ignored.
    socket.onmessage = (ev) => {
      if (this.socket === socket) this.receive(ev.data);
    };
    socket.onclose = (ev) => {
      if (this.socket === socket) this.closed(ev.code);
    };
    socket.onopen = () => {
      if (this.socket === socket) this.armWatchdog();
    };
    socket.onerror = null; // onclose always follows
  }

  private detach(): WebSocketLike | null {
    const socket = this.socket;
    this.socket = null;
    if (socket) socket.onopen = socket.onmessage = socket.onclose = socket.onerror = null;
    return socket;
  }

  private receive(raw: unknown): void {
    this.armWatchdog();
    const message = parseServerMessage(raw);
    if (message) this.handle(message);
  }

  private handle(message: ServerMessage): void {
    if (isEvent(message)) {
      this.advance(message.seq);
      if (this.seen.has(message.id)) return;
      this.remember(message.id);
      this.handlers.onEvent(message);
      return;
    }
    switch (message.type) {
      case "hello":
        this.attempt = 0;
        this.heartbeatMs = message.data.heartbeat_interval_ms;
        this.armWatchdog();
        if (this.lastSeq === null) {
          // No history: everything before hello comes from REST, everything after it live.
          this.lastSeq = message.data.seq;
          this.handlers.onResync();
        } else {
          this.resuming = true;
          this.pendingSeq = this.lastSeq;
          this.socket?.send(resumeMessage(this.lastSeq, this.o.newId(), this.o.now()));
        }
        return;
      case "resumed":
        this.finishResume(message.data.seq);
        return;
      case "resync_required":
        this.finishResume(message.data.seq);
        this.handlers.onResync();
        return;
      case "heartbeat":
        return;
    }
  }

  private finishResume(seq: number): void {
    const held = this.resuming ? this.pendingSeq : 0;
    this.resuming = false;
    this.advance(Math.max(seq, held));
  }

  private closed(code: number): void {
    this.resuming = false;
    this.detach();
    clearTimeout(this.watchdog);
    if (!this.running) return;
    if (code === CLOSE_CODES.sessionEnded) {
      this.running = false;
      this.handlers.onSessionEnded();
      return;
    }
    // A server bug (1008, 1009) or a per-user limit must not turn into a reconnect storm.
    const slow =
      code === CLOSE_CODES.tooManyConnections ||
      code === CLOSE_CODES.protocolViolation ||
      code === CLOSE_CODES.messageTooBig;
    this.scheduleReconnect(slow);
  }

  /** Equal jitter: half the capped exponential delay plus a random half, never zero. */
  private scheduleReconnect(slow: boolean): void {
    const cap = slow ? this.o.maxDelayMs : Math.min(this.o.maxDelayMs, this.o.baseDelayMs * 2 ** this.attempt);
    this.attempt += 1;
    const delay = cap / 2 + this.o.random() * (cap / 2);
    clearTimeout(this.reconnectTimer);
    this.reconnectTimer = setTimeout(() => {
      if (this.running) this.connect();
    }, delay);
  }

  /** No frame for two heartbeat periods: the connection is dead even if the browser has not noticed. */
  private armWatchdog(): void {
    clearTimeout(this.watchdog);
    this.watchdog = setTimeout(() => {
      this.resuming = false;
      const socket = this.detach();
      socket?.close(1000, "heartbeat timeout");
      if (this.running) this.scheduleReconnect(false);
    }, 2 * this.heartbeatMs);
  }

  private advance(seq: number): void {
    if (this.resuming) {
      this.pendingSeq = Math.max(this.pendingSeq, seq);
      return;
    }
    if (this.lastSeq === null || seq > this.lastSeq) this.lastSeq = seq;
  }

  private remember(id: string): void {
    this.seen.add(id);
    if (this.seen.size > SEEN_LIMIT) {
      const oldest = this.seen.values().next().value;
      if (oldest !== undefined) this.seen.delete(oldest);
    }
  }
}
