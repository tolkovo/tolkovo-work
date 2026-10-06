import { vi } from "vitest";

import type { SocketFactory } from "../realtime";

/** A file of a message, as the API returns it. */
export interface FakeFile {
  name: string;
  content_type: string;
  size: number;
  download: { method: string; url: string; headers: Record<string, string>; expires_at: string };
}

/** A message of the fake chat, as the API returns it. */
export interface FakeMessage {
  id: string;
  conversation_id: string;
  sender: "contact" | "operator" | "bot";
  body: string;
  created_at: string;
  files: FakeFile[];
}

interface FakeUpload {
  name: string;
  type: string;
  size: number;
  uploaded: boolean;
}

/** Where the fake's upload and download links point. */
const FILES = "https://files.example.com";

export function fakeFile(name: string, type: string, size: number, expiresAt = "2099-01-01T00:00:00Z", version = 1): FakeFile {
  return { name, content_type: type, size, download: { method: "GET", url: `${FILES}/d/${encodeURIComponent(name)}?v=${version}`, headers: {}, expires_at: expiresAt } };
}

export interface Call {
  method: string;
  path: string;
  headers: Record<string, string>;
  body: unknown;
}

/** UUIDv7-like ids that sort by creation, as the server's do. */
let seq = 0;
export function nextId(): string {
  seq++;
  return `0199f000-0000-7000-8000-${String(seq).padStart(12, "0")}`;
}

/**
 * The widget API on fetch: sessions, the chat, receipts. Tests change its state between steps
 * (an operator reply, an ended session) and read the calls the widget made.
 */
export class FakeServer {
  calls: Call[] = [];
  messages: FakeMessage[] = [];
  tokens = new Set<string>();
  identity: "verified" | "unverified" | "none" = "none";
  /** Someone on the team takes conversations. */
  operatorsOnline = true;
  /** The next answers of a route, before the normal behaviour. */
  failNext: { route: string; status: number; code: string }[] = [];
  uploads = new Map<string, FakeUpload>();
  /** Names of files the server finds not to be what their type says. */
  reject = new Set<string>();
  /** Bumped by tests to hand out new links of files. */
  linkVersion = 1;
  /** Where the widget reports goals (the panel's settings). */
  analytics = { data_layer: false, gtag: false, metrika_counter: null as string | null, goals: { chat_started: "tolkovo.chatStarted", contact_collected: "tolkovo.contactCollected", rated: "tolkovo.rated" } };
  /** The visitor's conversation: a new id after it was closed, resolved by the operator, rated by the visitor. */
  conversationId = "conv-1";
  resolved = false;
  rating: { score: number; comment: string | null; resolved_by: "human" | "ai"; rated_at: string } | null = null;
  private issued = 0;

  install(): void {
    vi.stubGlobal("fetch", (input: string, init: RequestInit = {}) => Promise.resolve(this.handle(input, init)));
    // jsdom has no object URLs.
    URL.createObjectURL = (b: Blob) => `blob:fake/${(b as File).name ?? "blob"}`;
    URL.revokeObjectURL = () => undefined;
  }

  reply(body: string, sender: "operator" | "bot" = "operator"): FakeMessage {
    const m: FakeMessage = { id: nextId(), conversation_id: this.conversationId, sender, body, created_at: "2026-10-05T10:00:00Z", files: [] };
    this.messages.push(m);
    return m;
  }

  private handle(url: string, init: RequestInit): Response {
    const u = new URL(url);
    const method = init.method ?? "GET";
    const headers = (init.headers ?? {}) as Record<string, string>;
    const body = typeof init.body === "string" ? (JSON.parse(init.body) as unknown) : undefined;
    if (u.origin === FILES) {
      this.calls.push({ method, path: u.pathname, headers, body: undefined });
      const up = this.uploads.get(u.pathname.replace("/u/", ""));
      if (method !== "PUT" || !up || up.uploaded || headers["Content-Type"] !== up.type) return json(403, { code: "link_rejected" });
      up.uploaded = true;
      return new Response(null, { status: 200 });
    }
    const path = u.pathname.replace("/api/v1/widget", "");
    this.calls.push({ method, path: path + u.search, headers, body });
    const route = `${method} ${path}`;
    const fail = this.failNext.findIndex((f) => f.route === route);
    if (fail >= 0) {
      const f = this.failNext.splice(fail, 1)[0]!;
      return json(f.status, { code: f.code });
    }
    const token = headers.Authorization?.replace("Bearer ", "") ?? "";
    if (route !== "POST /session" && !this.tokens.has(token)) {
      if (route === "DELETE /session") return new Response(null, { status: 204 });
      return json(401, { code: "widget_session_ended" });
    }
    switch (route) {
      case "POST /session": {
        const req = body as { session_token?: string; user?: unknown };
        let t = req.session_token;
        if (!t || !this.tokens.has(t)) {
          t = `tk_wsess_${++this.issued}`;
          this.tokens.add(t);
        }
        if (req.user) this.identity = "verified";
        return json(200, {
          session_token: t,
          expires_at: "2026-11-04T10:00:00Z",
          identity: this.identity,
          channel_name: "Поддержка Зерна",
          operators_online: this.operatorsOnline,
          analytics: this.analytics,
        });
      }
      case "DELETE /session":
        this.tokens.delete(token);
        return new Response(null, { status: 204 });
      case "GET /messages": {
        const since = u.searchParams.get("since") ?? "";
        const fresh = (m: FakeMessage) => ({ ...m, files: m.files.map((f) => ({ ...f, download: { ...f.download, url: f.download.url.replace(/v=\d+/, `v=${this.linkVersion}`), expires_at: this.linkVersion > 1 ? "2099-01-01T00:00:00Z" : f.download.expires_at } })) });
        const items = (since ? this.messages.filter((m) => m.id > since) : this.messages.slice(-100)).map(fresh);
        const last = items.at(-1)?.id ?? since;
        const conversation = this.messages.length ? { id: this.conversationId, status: this.resolved ? "resolved" : "open", rating: this.rating } : null;
        return json(200, { items, cursor: last, more: false, conversation });
      }
      case "POST /uploads": {
        const req = body as { file_name: string; content_type: string; size: number };
        const id = nextId();
        this.uploads.set(id, { name: req.file_name, type: req.content_type, size: req.size, uploaded: false });
        return json(201, { id, upload: { method: "PUT", url: `${FILES}/u/${id}`, headers: { "Content-Type": req.content_type }, expires_at: "2099-01-01T00:00:00Z" } });
      }
      case "POST /messages": {
        const req = body as { message?: string; attachment_ids?: string[] };
        const files: FakeFile[] = [];
        for (const id of req.attachment_ids ?? []) {
          const up = this.uploads.get(id);
          if (!up) return json(400, { code: "invalid_request" });
          if (!up.uploaded) return json(409, { code: "attachment_not_uploaded", upload_id: id });
          if (this.reject.has(up.name)) {
            this.uploads.delete(id);
            return json(422, { code: "file_type_mismatch", upload_id: id });
          }
          files.push(fakeFile(up.name, up.type, up.size));
        }
        // A message reopens a resolved conversation.
        this.resolved = false;
        const m: FakeMessage = {
          id: nextId(),
          conversation_id: this.conversationId,
          sender: "contact",
          body: req.message ?? "",
          created_at: "2026-10-05T10:00:00Z",
          files,
        };
        this.messages.push(m);
        return json(201, m);
      }
      case "POST /contact": {
        const req = body as { name?: string; email?: string; phone?: string };
        if (req.name || req.email || req.phone) this.identity = "unverified";
        return new Response(null, { status: 204 });
      }
      case "POST /receipts":
        return new Response(null, { status: 204 });
      case "GET /availability":
        return json(200, { operators_online: this.operatorsOnline });
      case "POST /rating": {
        const req = body as { conversation_id: string; score: number; comment?: string };
        if (req.conversation_id !== this.conversationId) return json(404, { code: "not_found" });
        if (!this.resolved) return json(409, { code: "conversation_open" });
        this.rating = { score: req.score, comment: req.comment ?? null, resolved_by: "human", rated_at: "2026-10-06T10:00:00Z" };
        return json(200, this.rating);
      }
    }
    return json(404, { code: "not_found" });
  }
}

function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

/** A WebSocket the test drives: it sees what the widget opened and sends frames to it. */
export class FakeSocket {
  static all: FakeSocket[] = [];
  readyState: 0 | 1 | 2 | 3 = 1;
  onopen: ((ev: Event) => void) | null = null;
  onmessage: ((ev: MessageEvent) => void) | null = null;
  onclose: ((ev: CloseEvent) => void) | null = null;
  onerror: ((ev: Event) => void) | null = null;
  closed: number | null = null;

  constructor(
    readonly url: string,
    readonly protocols: string[],
  ) {
    FakeSocket.all.push(this);
  }

  static factory: SocketFactory = (url, protocols) => new FakeSocket(url, protocols);

  static last(): FakeSocket {
    const s = FakeSocket.all.at(-1);
    if (!s) throw new Error("no socket opened");
    return s;
  }

  receive(type: string, data: unknown = {}): void {
    this.onmessage?.({ data: JSON.stringify({ type, id: "e1", ts: "2026-10-05T10:00:00Z", data }) } as MessageEvent);
  }

  /** The server closes the connection. */
  drop(code: number): void {
    this.readyState = 3;
    this.onclose?.({ code } as CloseEvent);
  }

  close(code?: number): void {
    this.readyState = 3;
    this.closed = code ?? 1000;
  }
}
