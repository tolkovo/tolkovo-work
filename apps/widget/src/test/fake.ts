import { vi } from "vitest";

import type { SocketFactory } from "../realtime";

/** A message of the fake chat, as the API returns it. */
export interface FakeMessage {
  id: string;
  conversation_id: string;
  sender: "contact" | "operator" | "bot";
  body: string;
  created_at: string;
  files: never[];
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
  /** The next answers of a route, before the normal behaviour. */
  failNext: { route: string; status: number; code: string }[] = [];
  private issued = 0;

  install(): void {
    vi.stubGlobal("fetch", (input: string, init: RequestInit = {}) => Promise.resolve(this.handle(input, init)));
  }

  reply(body: string, sender: "operator" | "bot" = "operator"): FakeMessage {
    const m: FakeMessage = { id: nextId(), conversation_id: "conv-1", sender, body, created_at: "2026-10-05T10:00:00Z", files: [] };
    this.messages.push(m);
    return m;
  }

  private handle(url: string, init: RequestInit): Response {
    const u = new URL(url);
    const method = init.method ?? "GET";
    const headers = (init.headers ?? {}) as Record<string, string>;
    const body = typeof init.body === "string" ? (JSON.parse(init.body) as unknown) : undefined;
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
        return json(200, { session_token: t, expires_at: "2026-11-04T10:00:00Z", identity: this.identity, channel_name: "Поддержка Зерна" });
      }
      case "DELETE /session":
        this.tokens.delete(token);
        return new Response(null, { status: 204 });
      case "GET /messages": {
        const since = u.searchParams.get("since") ?? "";
        const items = since ? this.messages.filter((m) => m.id > since) : this.messages.slice(-100);
        const last = items.at(-1)?.id ?? since;
        return json(200, { items, cursor: last, more: false });
      }
      case "POST /messages": {
        const m: FakeMessage = {
          id: nextId(),
          conversation_id: "conv-1",
          sender: "contact",
          body: (body as { message: string }).message,
          created_at: "2026-10-05T10:00:00Z",
          files: [],
        };
        this.messages.push(m);
        return json(201, m);
      }
      case "POST /receipts":
        return new Response(null, { status: 204 });
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
