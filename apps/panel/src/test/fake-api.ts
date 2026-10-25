import type { AISettings, DeviceSession, Member, Session, UpdateMeRequest, WorkspaceSummary } from "@tolkovo/api-client";
import { vi } from "vitest";

/** Test fixtures shaped by the generated API types, so a contract change breaks the tests. */
export const ownerSession: Session = {
  user: { id: "u-anna", email: "anna@zerno.test", name: "Анна Смирнова", locale: "ru", email_verified: true },
  workspace: { id: "w-zerno", slug: "zerno", name: "Зерно", locale: "ru", timezone: "Europe/Moscow" },
  role: "owner",
  permissions: [
    "channels.manage",
    "channels.view",
    "conversations.view",
    "integrations.manage",
    "members.manage",
    "members.view",
    "workspace.delete",
    "workspace.manage",
  ],
  csrf_token: "csrf-owner",
};

export const operatorSession: Session = {
  ...ownerSession,
  user: { id: "u-petr", email: "petr@zerno.test", name: "Пётр Иванов", locale: "ru", email_verified: true },
  role: "operator",
  permissions: ["conversations.view", "members.view"],
  csrf_token: "csrf-operator",
};

/** Works in the tasks section and reads only the conversations linked to tasks (ADR-0020). */
export const programmerSession: Session = {
  ...ownerSession,
  user: { id: "u-ivan", email: "ivan@zerno.test", name: "Иван Котов", locale: "ru", email_verified: true },
  role: "programmer",
  permissions: ["conversations.note", "conversations.view_linked", "members.view", "tasks.edit", "tasks.view"],
  csrf_token: "csrf-programmer",
};

/** AI never set up, as a new workspace has it. */
export const aiSettingsOff: AISettings = {
  enabled: false,
  provider: null,
  model: null,
  base_url: null,
  key: null,
  prices: { input_micros: 0, output_micros: 0 },
  monthly_budget_micros: null,
  spent_micros: 0,
  period_start: "2026-10-01T00:00:00Z",
  models: [
    { provider: "anthropic", model: "claude-haiku-4-5", prices: { input_micros: 1_000_000, output_micros: 5_000_000 } },
    { provider: "openai", model: "gpt-4.1-mini", prices: { input_micros: 400_000, output_micros: 1_600_000 } },
    { provider: "deepseek", model: "deepseek-chat", prices: { input_micros: 280_000, output_micros: 420_000 } },
  ],
  encryption_available: true,
};

export const members: Member[] = [
  { user_id: "u-anna", email: "anna@zerno.test", name: "Анна Смирнова", role: "owner", active: true, presence: "online" },
  { user_id: "u-petr", email: "petr@zerno.test", name: "Пётр Иванов", role: "operator", active: false, presence: "offline" },
];

export const workspaces: WorkspaceSummary[] = [{ id: "w-zerno", slug: "zerno", name: "Зерно", role: "owner" }];

export const deviceSessions: DeviceSession[] = [
  {
    id: "s-this",
    created_at: "2026-10-01T09:00:00Z",
    last_seen_at: "2026-10-04T10:00:00Z",
    user_agent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:131.0) Gecko/20100101 Firefox/131.0",
    current: true,
  },
  {
    id: "s-phone",
    created_at: "2026-09-20T08:00:00Z",
    last_seen_at: "2026-10-03T19:30:00Z",
    user_agent:
      "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1",
    current: false,
  },
];

type Handler = (request: Request) => Response | Promise<Response>;
export type FakeRoutes = Record<string, Handler>;

export function json(status: number, body: unknown, headers: Record<string, string> = {}): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": status >= 400 ? "application/problem+json" : "application/json", ...headers },
  });
}

export function problem(status: number, code: string, headers: Record<string, string> = {}): Response {
  return json(status, { type: "about:blank", title: "Error", status, code }, headers);
}

/** A JSON body as an object; a file upload stays its text. */
function parseBody(text: string): unknown {
  try {
    return JSON.parse(text) as unknown;
  } catch {
    return text;
  }
}

/**
 * A fake Tolkovo API: "METHOD /path" (path after /api/v1) → handler. Unknown routes fail the
 * test loudly. Every request is recorded, with its body read once, for assertions.
 */
export function fakeApi(
  { session = ownerSession, routes = {} }: { session?: Session | null; routes?: Record<string, Handler> } = {},
) {
  const calls: { method: string; path: string; headers: Headers; body: unknown }[] = [];
  const defaults: Record<string, Handler> = {
    "GET /me": () => (session ? json(200, session) : problem(401, "unauthenticated")),
    "GET /me/workspaces": () => json(200, { items: workspaces }),
    "GET /auth/registration": () => json(200, { open: true }),
    "GET /members": () => json(200, { items: members }),
    "GET /me/sessions": () => json(200, { items: deviceSessions }),
    "GET /invitations": () => json(200, { items: [], email_enabled: true }),
    "GET /conversations": () => json(200, { items: [], next_cursor: null }),
    "GET /conversations/counts": () => json(200, { mine: 0, mine_unread: 0, unassigned: 0, open: 0 }),
    "GET /me/notifications": () => json(200, { email_missed: true, email_delay_minutes: 15, email_available: true, telegram_assigned: true, telegram_messages: true, telegram_queue: true }),
    "GET /me/presence": () => json(200, { status: "online" }),
    "GET /routing": () => json(200, { mode: "manual", max_chats: null }),
    "GET /routing/members": () => json(200, { items: [{ user_id: "u-anna", max_chats: null, open: 1 }] }),
    "GET /ai/settings": () => json(200, aiSettingsOff),
    "GET /ai/bot": () =>
      json(200, { channel_ids: [], topics_off: [], min_confidence: 75, handoff_message: "", name: "", tone: "friendly", address: "formal", rules: "" }),
    "GET /tags": () => json(200, { items: [] }),
    "GET /task-tags": () => json(200, { items: [] }),
    "GET /topics": () => json(200, { items: [] }),
    "GET /lines": () => json(200, { items: [] }),
    "GET /channels": () => json(200, { items: [], telegram_user_available: true, encryption_available: true }),
    // Like the server: absent fields stay, the answer is the session with the changed user.
    "PATCH /me": async (request) => {
      if (!session) return problem(401, "unauthenticated");
      const body = (await request.json()) as UpdateMeRequest;
      return json(200, { ...session, user: { ...session.user, ...body } });
    },
    "POST /auth/logout": () => new Response(null, { status: 204 }),
  };
  const table = { ...defaults, ...routes };
  const fetch = vi.fn(async (request: Request) => {
    const url = new URL(request.url);
    const path = url.pathname.replace(/^\/api\/v1/, "");
    const text = await request.clone().text();
    calls.push({
      method: request.method,
      path,
      headers: request.headers,
      body: text ? parseBody(text) : undefined,
    });
    const handler = table[`${request.method} ${path}`];
    if (!handler) throw new Error(`fakeApi: no handler for ${request.method} ${path}`);
    return handler(request);
  });
  return { fetch, calls };
}
