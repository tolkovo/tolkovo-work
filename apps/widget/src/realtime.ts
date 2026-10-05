/**
 * The widget's realtime connection (api/ws/widget-message.schema.json). It only listens: after
 * hello, resync_required or widget.message the chat is refetched over REST, so a lost event costs
 * a moment, never a message. Reconnects with exponential backoff 0.5 → 30 s and equal jitter, as
 * the panel does (docs/02 §10.1).
 */

export const SUBPROTOCOL = "tolkovo.widget.v1";

const MIN_DELAY = 500;
const MAX_DELAY = 30_000;

/** Close codes after which only a long backoff makes sense (a broken client, too many tabs). */
const LONG_BACKOFF = new Set([1008, 4429]);
/** The session ended: the widget starts a new one before reconnecting. */
const SESSION_ENDED = 4401;

export interface RealtimeHandlers {
  /** Refetch the chat: connected, or events may have been missed. */
  onRefetch(): void;
  /** An operator is typing. */
  onTyping(): void;
  /** The session ended (4401); the widget starts a new one and calls connect again. */
  onSessionEnded(): void;
  /** Connected or not, for the «no connection» line. */
  onStatus(online: boolean): void;
}

type Socket = Pick<WebSocket, "close" | "readyState"> & {
  onopen: ((ev: Event) => void) | null;
  onmessage: ((ev: MessageEvent) => void) | null;
  onclose: ((ev: CloseEvent) => void) | null;
  onerror: ((ev: Event) => void) | null;
};

export type SocketFactory = (url: string, protocols: string[]) => Socket;

export class Realtime {
  private ws: Socket | null = null;
  private attempt = 0;
  private retryTimer: ReturnType<typeof setTimeout> | undefined;
  private watchdog: ReturnType<typeof setTimeout> | undefined;
  private heartbeatMs = 25_000;
  private stopped = true;
  private token = "";

  constructor(
    private readonly url: string,
    private readonly handlers: RealtimeHandlers,
    private readonly factory: SocketFactory = (u, p) => new WebSocket(u, p),
    private readonly random: () => number = Math.random,
  ) {}

  /** Connects with the session's token; a connection already open is replaced. */
  connect(token: string): void {
    this.stop();
    this.stopped = false;
    this.token = token;
    this.open();
  }

  /** Closes the connection and stops reconnecting. */
  stop(): void {
    this.stopped = true;
    clearTimeout(this.retryTimer);
    clearTimeout(this.watchdog);
    const ws = this.ws;
    this.ws = null;
    if (ws) {
      ws.onclose = ws.onmessage = ws.onerror = ws.onopen = null;
      ws.close(1000);
    }
  }

  private open(): void {
    let ws: Socket;
    try {
      ws = this.factory(this.url, [SUBPROTOCOL, this.token]);
    } catch {
      this.schedule(false);
      return;
    }
    this.ws = ws;
    ws.onmessage = (ev) => this.onMessage(ev);
    ws.onclose = (ev) => this.onClose(ev.code);
    ws.onerror = () => {
      // onclose follows with the code.
    };
  }

  private onMessage(ev: MessageEvent): void {
    this.arm();
    let msg: { type?: unknown; data?: { heartbeat_interval_ms?: unknown } };
    try {
      msg = JSON.parse(String(ev.data)) as typeof msg;
    } catch {
      return;
    }
    switch (msg.type) {
      case "hello":
        if (typeof msg.data?.heartbeat_interval_ms === "number") this.heartbeatMs = msg.data.heartbeat_interval_ms;
        this.attempt = 0;
        this.arm();
        this.handlers.onStatus(true);
        this.handlers.onRefetch();
        break;
      case "resync_required":
      case "widget.message":
        this.handlers.onRefetch();
        break;
      case "widget.typing":
        this.handlers.onTyping();
        break;
    }
  }

  /** A connection that hears nothing for two heartbeats is dead: reconnect. */
  private arm(): void {
    clearTimeout(this.watchdog);
    this.watchdog = setTimeout(() => {
      const ws = this.ws;
      if (!ws) return;
      ws.onclose = null;
      ws.close(4000);
      this.onClose(1006);
    }, this.heartbeatMs * 2);
  }

  private onClose(code: number): void {
    clearTimeout(this.watchdog);
    this.ws = null;
    this.handlers.onStatus(false);
    if (this.stopped) return;
    if (code === SESSION_ENDED) {
      this.stopped = true;
      this.handlers.onSessionEnded();
      return;
    }
    this.schedule(LONG_BACKOFF.has(code));
  }

  /** Equal jitter: half the delay plus a random half (docs/02 §10.1). */
  private schedule(long: boolean): void {
    const base = long ? MAX_DELAY : Math.min(MAX_DELAY, MIN_DELAY * 2 ** this.attempt);
    this.attempt++;
    const delay = base / 2 + (this.random() * base) / 2;
    clearTimeout(this.retryTimer);
    this.retryTimer = setTimeout(() => {
      if (!this.stopped) this.open();
    }, delay);
  }
}
