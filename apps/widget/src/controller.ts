import { WidgetApi, WidgetApiError, isSessionEnded, type WidgetMessage } from "./api";
import { Realtime, type SocketFactory } from "./realtime";
import type { ChatMessage, EventName, Events, Handler, TokenStorage, User } from "./types";

/**
 * The widget's state and its conversation with the server, apart from the UI. The session token
 * is kept per widget key; an anonymous session creates nothing in the inbox until the first
 * message (docs/02 §13.2).
 */

/** A message the visitor sent that the server has not confirmed yet. */
export interface Pending {
  key: string;
  body: string;
  failed: boolean;
}

export interface State {
  /** starting — no session yet; ready — the chat works; unavailable — the server refused the widget. */
  status: "starting" | "ready" | "unavailable";
  online: boolean;
  open: boolean;
  launcher: boolean;
  /** The widget's name in the panel. */
  channelName: string;
  messages: ChatMessage[];
  pending: Pending[];
  /** An operator typed within the last 6 s. */
  typing: boolean;
  unread: number;
}

const TYPING_MS = 6_000;
const MAX_CHARS = 10_000;

/** Memory storage when localStorage is not available (private mode, blocked storage). */
function memoryStorage(): TokenStorage {
  const m = new Map<string, string>();
  return { getItem: (k) => m.get(k) ?? null, setItem: (k, v) => void m.set(k, v), removeItem: (k) => void m.delete(k) };
}

function defaultStorage(): TokenStorage {
  try {
    const s = window.localStorage;
    const probe = "tolkovo:probe";
    s.setItem(probe, "1");
    s.removeItem(probe);
    return s;
  } catch {
    return memoryStorage();
  }
}

function newKey(): string {
  return typeof crypto !== "undefined" && "randomUUID" in crypto
    ? crypto.randomUUID()
    : `${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`;
}

function toChat(m: WidgetMessage): ChatMessage {
  return { id: m.id, sender: m.sender, body: m.body, createdAt: m.created_at };
}

export interface ControllerOptions {
  widgetKey: string;
  apiBase: string;
  user?: User | undefined;
  storage?: TokenStorage | undefined;
  open?: boolean | undefined;
  launcher?: boolean | undefined;
  /** Tests replace the WebSocket. */
  socket?: SocketFactory;
}

export class Controller {
  private state: State;
  private readonly listeners = new Set<() => void>();
  private readonly handlers = new Map<EventName, Set<Handler<EventName>>>();
  private readonly api: WidgetApi;
  private readonly realtime: Realtime;
  private readonly storage: TokenStorage;
  private readonly storageKey: string;
  private token: string | null;
  private user: User | undefined;
  private cursor = "";
  private fetching: Promise<void> | null = null;
  private again = false;
  private typingTimer: ReturnType<typeof setTimeout> | undefined;
  private delivered = "";
  private read = "";
  private destroyed = false;
  private started: Promise<void> | null = null;

  constructor(private readonly o: ControllerOptions) {
    this.api = new WidgetApi(o.apiBase);
    this.storage = o.storage ?? defaultStorage();
    this.storageKey = `tolkovo:${o.widgetKey}:session`;
    this.token = this.safeGet();
    this.user = o.user;
    this.state = {
      status: "starting",
      online: true,
      open: o.open ?? false,
      launcher: o.launcher ?? true,
      channelName: "",
      messages: [],
      pending: [],
      typing: false,
      unread: 0,
    };
    this.realtime = new Realtime(
      this.api.realtimeUrl(),
      {
        onRefetch: () => void this.refetch(),
        onTyping: () => this.showTyping(),
        onSessionEnded: () => void this.restart(),
        onStatus: (online) => this.set({ online }),
      },
      o.socket,
    );
  }

  // --- store ---

  get(): State {
    return this.state;
  }

  subscribe(fn: () => void): () => void {
    this.listeners.add(fn);
    return () => this.listeners.delete(fn);
  }

  private set(patch: Partial<State>): void {
    this.state = { ...this.state, ...patch };
    for (const fn of this.listeners) fn();
  }

  on<E extends EventName>(event: E, handler: Handler<E>): void {
    let set = this.handlers.get(event);
    if (!set) this.handlers.set(event, (set = new Set()));
    set.add(handler as Handler<EventName>);
  }

  off<E extends EventName>(event: E, handler: Handler<E>): void {
    this.handlers.get(event)?.delete(handler as Handler<EventName>);
  }

  private emit<E extends EventName>(event: E, payload: Events[E]): void {
    for (const h of this.handlers.get(event) ?? []) {
      try {
        (h as Handler<E>)(payload);
      } catch {
        // A host's handler must not break the widget.
      }
    }
  }

  // --- session ---

  private safeGet(): string | null {
    try {
      return this.storage.getItem(this.storageKey);
    } catch {
      return null;
    }
  }

  private saveToken(token: string | null): void {
    this.token = token;
    try {
      if (token) this.storage.setItem(this.storageKey, token);
      else this.storage.removeItem(this.storageKey);
    } catch {
      // The token lives in memory then: the next start opens a new session.
    }
  }

  /** Starts or continues the session, loads the chat, connects. */
  start(): Promise<void> {
    this.started ??= this.begin();
    return this.started;
  }

  private async begin(): Promise<void> {
    try {
      await this.openSession();
    } catch (e) {
      this.set({ status: "unavailable" });
      if (!(e instanceof WidgetApiError)) this.set({ online: false });
      return;
    }
    if (this.destroyed) return;
    this.set({ status: "ready" });
    await this.refetch();
    this.realtime.connect(this.token!);
    this.emit("ready", undefined);
  }

  private async openSession(): Promise<void> {
    const u = this.user;
    const session = await this.api.startSession({
      widget_key: this.o.widgetKey,
      ...(this.token ? { session_token: this.token } : {}),
      ...(u ? { user: { external_id: u.id, ...(u.hash ? { user_hash: u.hash } : {}), ...(u.name ? { name: u.name } : {}), ...(u.email ? { email: u.email } : {}) } } : {}),
    });
    if (session.session_token !== this.token) this.resetChat();
    this.saveToken(session.session_token);
    this.set({ channelName: session.channel_name });
  }

  /** The session ended on the server: a new one, a fresh chat, a new connection. */
  private async restart(): Promise<void> {
    this.saveToken(null);
    this.resetChat();
    try {
      await this.openSession();
    } catch {
      this.set({ status: "unavailable" });
      return;
    }
    await this.refetch();
    this.realtime.connect(this.token!);
  }

  private resetChat(): void {
    this.cursor = "";
    this.delivered = this.read = "";
    this.set({ messages: [], pending: [], unread: 0, typing: false });
  }

  async identify(user: User): Promise<void> {
    this.user = user;
    await this.start();
    if (this.state.status !== "ready") return;
    const before = this.token;
    await this.openSession();
    // The same token may now be another contact's: the chat is loaded afresh either way.
    if (before === this.token) this.resetChat();
    await this.refetch();
    this.realtime.connect(this.token!);
  }

  async logout(): Promise<void> {
    const token = this.token;
    this.user = undefined;
    this.realtime.stop();
    this.saveToken(null);
    this.resetChat();
    if (token) {
      try {
        await this.api.endSession(token);
      } catch {
        // The token is forgotten here anyway; it expires on the server.
      }
    }
    this.started = null;
    await this.start();
  }

  // --- chat ---

  /** Loads what is new; calls during a load run once more after it. */
  refetch(): Promise<void> {
    if (this.fetching) {
      this.again = true;
      return this.fetching;
    }
    this.fetching = (async () => {
      do {
        this.again = false;
        await this.loadPage();
      } while (this.again && !this.destroyed);
    })().finally(() => {
      this.fetching = null;
    });
    return this.fetching;
  }

  private async loadPage(): Promise<void> {
    const token = this.token;
    if (!token || this.destroyed) return;
    let more = true;
    while (more) {
      let page;
      try {
        page = await this.api.messages(token, this.cursor);
      } catch (e) {
        if (isSessionEnded(e)) void this.restart();
        return;
      }
      more = page.more;
      this.cursor = page.cursor;
      this.merge(page.items.map(toChat));
    }
    this.sendReceipts();
  }

  private merge(items: ChatMessage[]): void {
    const known = new Set(this.state.messages.map((m) => m.id));
    const fresh = items.filter((m) => !known.has(m.id));
    if (fresh.length === 0) return;
    const messages = [...this.state.messages, ...fresh].sort((a, b) => (a.id < b.id ? -1 : a.id > b.id ? 1 : 0));
    const replies = fresh.filter((m) => m.sender !== "contact");
    const patch: Partial<State> = { messages };
    if (replies.length > 0) {
      patch.typing = false;
      if (!this.state.open || document.visibilityState === "hidden") patch.unread = this.state.unread + replies.length;
    }
    this.set(patch);
    for (const m of replies) this.emit("message", m);
    if (patch.unread !== undefined) this.emit("unread", patch.unread);
  }

  /** Delivered: the widget has the replies; read: the open window shows them. */
  sendReceipts(): void {
    const token = this.token;
    const last = [...this.state.messages].reverse().find((m) => m.sender !== "contact");
    if (!token || !last) return;
    const visible = this.state.open && document.visibilityState !== "hidden";
    const body: { delivered_up_to?: string; read_up_to?: string } = {};
    if (last.id > this.delivered) body.delivered_up_to = last.id;
    if (visible && last.id > this.read) body.read_up_to = last.id;
    if (!body.delivered_up_to && !body.read_up_to) return;
    if (body.delivered_up_to) this.delivered = last.id;
    if (body.read_up_to) this.read = last.id;
    this.api.receipt(token, body).catch(() => {
      // A lost receipt costs a status in the panel, nothing more.
    });
  }

  async send(text: string, key = newKey()): Promise<void> {
    const body = text.trim();
    if (!body || body.length > MAX_CHARS) return;
    const firstMessage = this.state.messages.length === 0 && this.state.pending.length === 0;
    this.set({ pending: [...this.state.pending.filter((p) => p.key !== key), { key, body, failed: false }] });
    await this.start();
    const attempt = async () => {
      const token = this.token;
      if (!token) throw new WidgetApiError(0, "no_session", null);
      return this.api.send(token, key, body);
    };
    let sent: WidgetMessage;
    try {
      try {
        sent = await attempt();
      } catch (e) {
        if (!isSessionEnded(e)) throw e;
        await this.restart();
        sent = await attempt();
      }
    } catch {
      this.set({ pending: this.state.pending.map((p) => (p.key === key ? { ...p, failed: true } : p)) });
      return;
    }
    this.set({ pending: this.state.pending.filter((p) => p.key !== key) });
    this.merge([toChat(sent)]);
    if (firstMessage) {
      this.emit("chatStarted", undefined);
      // The session has a contact now: its connection must follow it at once.
      this.realtime.connect(this.token!);
    }
  }

  retry(key: string): Promise<void> {
    const p = this.state.pending.find((x) => x.key === key);
    return p ? this.send(p.body, key) : Promise.resolve();
  }

  private showTyping(): void {
    clearTimeout(this.typingTimer);
    this.set({ typing: true });
    this.typingTimer = setTimeout(() => this.set({ typing: false }), TYPING_MS);
  }

  // --- window ---

  setOpen(open: boolean): void {
    if (open === this.state.open) return;
    this.set({ open, ...(open ? { unread: 0 } : {}) });
    if (open) {
      this.emit("unread", 0);
      this.sendReceipts();
    }
    this.emit(open ? "open" : "close", undefined);
  }

  setLauncher(launcher: boolean): void {
    this.set({ launcher });
  }

  /** The tab came back: catch up and mark what is now seen. */
  onVisible(): void {
    if (document.visibilityState !== "visible") return;
    if (this.state.open && this.state.unread > 0) {
      this.set({ unread: 0 });
      this.emit("unread", 0);
    }
    void this.refetch();
  }

  destroy(): void {
    this.destroyed = true;
    this.realtime.stop();
    clearTimeout(this.typingTimer);
    this.listeners.clear();
    this.handlers.clear();
  }
}
