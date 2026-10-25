import { type Goal, pagePath, report } from "./analytics";
import { WidgetApi, WidgetApiError, isSessionEnded, type ConversationRating, type WidgetAnalytics, type WidgetContactRequest, type WidgetMessage } from "./api";
import { MAX_FILE_SIZE, MAX_FILES, fileType, isImage } from "./files";
import { Realtime, type SocketFactory } from "./realtime";
import type { ChatMessage, EventName, Events, Handler, TokenStorage, User } from "./types";

/**
 * The widget's state and its conversation with the server, apart from the UI. The session token
 * is kept per widget key; an anonymous session creates nothing in the inbox until the first
 * message (docs/02 §13.2).
 */

/** A file of a message the server has not confirmed yet. */
export interface OutFile {
  file: File;
  name: string;
  /** The type declared to the server. */
  type: string;
  size: number;
  /** An object URL of a picture, for its preview. */
  preview?: string;
  /** Set once uploaded in the current session, so a retry does not upload it again. */
  uploadId?: string;
}

/** A message the visitor sent that the server has not confirmed yet. */
export interface Pending {
  key: string;
  body: string;
  files: OutFile[];
  failed: boolean;
  /** rejected — the server refused the file `rejected` and deleted it; unavailable — files cannot be checked now. */
  error?: "rejected" | "unavailable";
  rejected?: string;
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
  /** The pre-chat form is shown instead of the message field. */
  intro: boolean;
  /** Someone on the team takes conversations now; without them the UI offers the offline form. */
  operatorsOnline: boolean;
  /** The visitor's newest conversation; resolved — the UI asks for a rating. Null before the first message. */
  conversation: { id: string; resolved: boolean; rating: ConversationRating | null; topicId: string | null; withBot: boolean } | null;
  /** Topics of questions to pick from before writing (docs/02 §7.3); empty asks none. */
  topics: { id: string; name: string }[];
  /** The topic picked before the conversation has one: it goes with the next message. */
  topic: string | null;
}

const TYPING_MS = 6_000;
/** Whether someone is there is asked again on opening, at most this often. */
const AVAILABILITY_MS = 30_000;
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

/** A short mark of the session the form was filled in for; not the token itself. */
const mark = (token: string) => token.slice(-12);

function newKey(): string {
  return typeof crypto !== "undefined" && "randomUUID" in crypto
    ? crypto.randomUUID()
    : `${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`;
}

function toChat(m: WidgetMessage): ChatMessage {
  return {
    id: m.id,
    sender: m.sender,
    body: m.body,
    createdAt: m.created_at,
    files: m.files.map((f) => ({ name: f.name, contentType: f.content_type, size: f.size, url: f.download.url, expiresAt: f.download.expires_at })),
  };
}

function revoke(files: OutFile[]): void {
  for (const f of files) if (f.preview) URL.revokeObjectURL(f.preview);
}

/** Links of files are renewed this long before they run out. */
const RENEW_BEFORE_MS = 60_000;

export interface ControllerOptions {
  widgetKey: string;
  apiBase: string;
  user?: User | undefined;
  storage?: TokenStorage | undefined;
  open?: boolean | undefined;
  launcher?: boolean | undefined;
  /** The site asks for the pre-chat form. */
  prechat?: boolean | undefined;
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
  private renewTimer: ReturnType<typeof setTimeout> | undefined;
  private renewing: Promise<void> | null = null;
  private checkedAt = 0;
  private analytics: WidgetAnalytics | null = null;

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
      // Until the session says otherwise, a site with the form shows it.
      intro: o.prechat ?? false,
      operatorsOnline: true,
      conversation: null,
      topics: [],
      topic: null,
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

  /** A goal: the host's handlers and the site's analytics hear it. */
  private goal<E extends Goal>(event: E, payload: Events[E]): void {
    this.emit(event, payload);
    report(this.analytics, event, payload);
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
    return this.storageGet(this.storageKey);
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
    // A visitor the site identified, or who filled in the form, is not asked again.
    const intro = !!this.o.prechat && session.identity === "none" && this.storageGet(this.introKey) !== mark(session.session_token);
    this.checkedAt = Date.now();
    this.analytics = session.analytics;
    this.set({ channelName: session.channel_name, intro, operatorsOnline: session.operators_online, topics: session.topics });
  }

  /** On opening the window a while after the start: is someone there now? */
  private checkAvailability(): void {
    const token = this.token;
    if (!token || this.state.status !== "ready" || Date.now() - this.checkedAt < AVAILABILITY_MS) return;
    this.checkedAt = Date.now();
    this.api.availability(token).then(
      (a) => this.set({ operatorsOnline: a.operators_online }),
      () => {
        // The last answer stands; an ended session is noticed by the next call.
      },
    );
  }

  private get introKey(): string {
    return this.storageKey.replace(/:session$/, ":intro");
  }

  private storageGet(key: string): string | null {
    try {
      return this.storage.getItem(key);
    } catch {
      return null;
    }
  }

  /** Sends the pre-chat form; nothing filled in just closes it. Throws when the server did not take it. */
  async introduce(form: WidgetContactRequest): Promise<void> {
    await this.start();
    if (this.state.status !== "ready") throw new WidgetApiError(0, "unavailable", null);
    const filled = Object.keys(form).length > 0;
    if (filled) {
      const attempt = () => {
        if (!this.token) throw new WidgetApiError(0, "no_session", null);
        return this.api.contact(this.token, form);
      };
      try {
        await attempt();
      } catch (e) {
        if (!isSessionEnded(e)) throw e;
        await this.restart();
        await attempt();
      }
    }
    try {
      if (this.token) this.storage.setItem(this.introKey, mark(this.token));
    } catch {
      // Not kept: the server knows, the next session start says so.
    }
    this.set({ intro: false });
    if (filled) this.goal("contactCollected", { source: "prechat" });
  }

  /**
   * Sends the offline form: the contact first, then the message, as the visitor's first one. Throws
   * when the server did not take the contact; a message that fails stays in the chat to retry.
   */
  async leaveMessage(form: WidgetContactRequest, text: string): Promise<void> {
    await this.start();
    if (this.state.status !== "ready") throw new WidgetApiError(0, "unavailable", null);
    const attempt = () => {
      if (!this.token) throw new WidgetApiError(0, "no_session", null);
      return this.api.contact(this.token, { ...form, form: "offline" });
    };
    try {
      await attempt();
    } catch (e) {
      if (!isSessionEnded(e)) throw e;
      await this.restart();
      await attempt();
    }
    try {
      // The pre-chat form does not ask again: the visitor just said who they are.
      if (this.token) this.storage.setItem(this.introKey, mark(this.token));
    } catch {
      // Not kept: the server knows, the next session start says so.
    }
    this.set({ intro: false });
    this.goal("contactCollected", { source: "offline" });
    await this.send(text);
  }

  /** The session ended on the server: a new one, a fresh chat, a new connection. Unsent messages stay: they are the same visitor's. */
  private async restart(): Promise<void> {
    this.saveToken(null);
    this.resetChat(true);
    // Uploads belong to the session that made them.
    for (const p of this.state.pending) for (const f of p.files) delete f.uploadId;
    try {
      await this.openSession();
    } catch {
      this.set({ status: "unavailable" });
      return;
    }
    await this.refetch();
    this.realtime.connect(this.token!);
  }

  private resetChat(keepPending = false): void {
    this.cursor = "";
    this.delivered = this.read = "";
    if (!keepPending) for (const p of this.state.pending) revoke(p.files);
    this.set({ messages: [], unread: 0, typing: false, conversation: null, ...(keepPending ? {} : { pending: [] }) });
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
      const c = page.conversation;
      this.set({
        conversation: c ? { id: c.id, resolved: c.status === "resolved", rating: c.rating, topicId: c.topic_id, withBot: c.with_bot } : null,
      });
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
    if (fresh.some((m) => m.files.length > 0)) queueMicrotask(() => this.scheduleRenew());
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

  /** Sends text, files or both. Files the server would not accept are not sent at all: the UI checks them first (files.ts). */
  async send(text: string, files: File[] = [], key = newKey()): Promise<void> {
    const body = text.trim();
    if ((!body && files.length === 0) || body.length > MAX_CHARS || files.length > MAX_FILES) return;
    const out: OutFile[] = [];
    for (const file of files) {
      const type = fileType(file);
      if (!type || file.size <= 0 || file.size > MAX_FILE_SIZE) return;
      out.push({ file, name: file.name, type, size: file.size, ...(isImage(type) ? { preview: URL.createObjectURL(file) } : {}) });
    }
    this.set({ pending: [...this.state.pending.filter((p) => p.key !== key), { key, body, files: out, failed: false }] });
    await this.start();
    await this.deliver(key);
  }

  /** Uploads what is not uploaded yet, then sends the message. A retry with the same key never sends it twice. */
  private async deliver(key: string): Promise<void> {
    const p = this.state.pending.find((x) => x.key === key);
    if (!p) return;
    const attempt = async () => {
      const token = this.token;
      if (!token) throw new WidgetApiError(0, "no_session", null);
      const ids: string[] = [];
      for (const f of p.files) {
        if (!f.uploadId) {
          const up = await this.api.createUpload(token, { file_name: f.name, content_type: f.type, size: f.size });
          await this.api.put(up.upload, f.file);
          f.uploadId = up.id;
        }
        ids.push(f.uploadId);
      }
      return this.api.send(token, key, p.body, ids, this.state.conversation?.topicId ? null : this.state.topic);
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
    } catch (e) {
      this.fail(p, e);
      return;
    }
    revoke(p.files);
    const firstMessage = !this.state.messages.some((m) => m.sender === "contact");
    // A new conversation: the first one, or one after the last was closed. A resolved one reopens.
    const started = this.state.conversation?.id !== sent.conversation_id;
    const topicId = (started ? null : this.state.conversation!.topicId) ?? this.state.topic;
    this.set({
      pending: this.state.pending.filter((x) => x.key !== key),
      conversation: started
        ? { id: sent.conversation_id, resolved: false, rating: null, topicId, withBot: false }
        : { ...this.state.conversation!, resolved: false, topicId },
      topic: null,
    });
    this.merge([toChat(sent)]);
    if (started) this.goal("chatStarted", { channel: "widget", page: pagePath() });
    // The session has a contact now: its connection must follow it at once.
    if (firstMessage) this.realtime.connect(this.token!);
  }

  /**
   * Picks the topic of the question: for the open conversation at once, otherwise for the next
   * message. Throws when the server did not take it.
   */
  async chooseTopic(id: string): Promise<void> {
    const c = this.state.conversation;
    if (!c || c.resolved || !this.token) {
      this.set({ topic: this.state.topic === id ? null : id });
      return;
    }
    try {
      await this.api.topic(this.token, { conversation_id: c.id, topic_id: id });
    } catch (e) {
      if (isSessionEnded(e)) void this.restart();
      throw e;
    }
    if (this.state.conversation?.id === c.id) this.set({ conversation: { ...this.state.conversation, topicId: id } });
  }

  /** Rates the resolved conversation. Throws when the server did not take it; a conversation that reopened stops asking. */
  async rate(score: number, comment: string): Promise<void> {
    const c = this.state.conversation;
    if (!this.token || !c) throw new WidgetApiError(0, "no_conversation", null);
    const text = comment.trim();
    let rating: ConversationRating;
    try {
      rating = await this.api.rate(this.token, { conversation_id: c.id, score, ...(text ? { comment: text } : {}) });
    } catch (e) {
      if (e instanceof WidgetApiError && e.code === "conversation_open") void this.refetch();
      if (isSessionEnded(e)) void this.restart();
      throw e;
    }
    if (this.state.conversation?.id === c.id) this.set({ conversation: { ...this.state.conversation, rating } });
    this.goal("rated", { score: rating.score, resolvedBy: rating.resolved_by });
  }

  /** Marks a message as not sent, with what the visitor can do about it. */
  private fail(p: Pending, e: unknown): void {
    const patch: Partial<Pending> = { failed: true };
    if (e instanceof WidgetApiError) {
      const file = e.uploadId ? p.files.find((f) => f.uploadId === e.uploadId) : undefined;
      if (file && (e.code === "file_type_mismatch" || e.code === "file_infected")) {
        // The server deleted the file: the message can go on without it.
        revoke([file]);
        patch.files = p.files.filter((f) => f !== file);
        patch.error = "rejected";
        patch.rejected = file.name;
      } else if (file && e.code === "attachment_not_uploaded") {
        delete file.uploadId;
      } else if (e.code === "scan_unavailable") {
        patch.error = "unavailable";
      }
    }
    this.set({ pending: this.state.pending.map((x) => (x.key === p.key ? { ...x, ...patch } : x)) });
  }

  retry(key: string): Promise<void> {
    const p = this.state.pending.find((x) => x.key === key);
    if (!p) return Promise.resolve();
    if (!p.body && p.files.length === 0) {
      this.dismiss(key);
      return Promise.resolve();
    }
    const { error: _error, rejected: _rejected, ...rest } = p;
    this.set({ pending: this.state.pending.map((x) => (x.key === key ? { ...rest, failed: false } : x)) });
    return this.deliver(key);
  }

  /** Drops a message that was not sent. */
  dismiss(key: string): void {
    const p = this.state.pending.find((x) => x.key === key);
    if (!p) return;
    revoke(p.files);
    this.set({ pending: this.state.pending.filter((x) => x.key !== key) });
  }

  // --- links of files ---

  /** The earliest expiry of a file link, ms; Infinity without files. */
  private soonestExpiry(): number {
    let soonest = Infinity;
    for (const m of this.state.messages) for (const f of m.files) soonest = Math.min(soonest, Date.parse(f.expiresAt));
    return soonest;
  }

  /** Renews the links of files of the newest messages; a picture that failed to load asks for it too. */
  renewFiles(): Promise<void> {
    const token = this.token;
    if (!token) return Promise.resolve();
    this.renewing ??= (async () => {
      try {
        const page = await this.api.messages(token, "");
        const fresh = new Map(page.items.map((m) => [m.id, toChat(m).files]));
        this.set({ messages: this.state.messages.map((m) => (fresh.has(m.id) ? { ...m, files: fresh.get(m.id)! } : m)) });
      } catch {
        // The next timer tries again.
      }
    })().finally(() => {
      this.renewing = null;
    });
    return this.renewing;
  }

  /** While the window is open, links are renewed a minute before they run out. */
  private scheduleRenew(): void {
    clearTimeout(this.renewTimer);
    if (!this.state.open || this.destroyed) return;
    const soonest = this.soonestExpiry();
    if (soonest === Infinity) return;
    // At most 10 minutes: a timer longer than 2^31 ms fires at once.
    const delay = Math.min(10 * 60_000, Math.max(30_000, soonest - Date.now() - RENEW_BEFORE_MS));
    this.renewTimer = setTimeout(() => void this.renewFiles().then(() => this.scheduleRenew()), delay);
  }

  /** On opening: links that ran out meanwhile are renewed at once. */
  private renewIfStale(): void {
    if (this.soonestExpiry() - Date.now() < RENEW_BEFORE_MS) void this.renewFiles().then(() => this.scheduleRenew());
    else this.scheduleRenew();
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
      this.renewIfStale();
      this.checkAvailability();
    } else {
      clearTimeout(this.renewTimer);
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
    if (this.state.open) this.renewIfStale();
  }

  destroy(): void {
    this.destroyed = true;
    this.realtime.stop();
    clearTimeout(this.typingTimer);
    clearTimeout(this.renewTimer);
    for (const p of this.state.pending) revoke(p.files);
    this.listeners.clear();
    this.handlers.clear();
  }
}
