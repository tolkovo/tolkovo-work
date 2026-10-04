import type { Member, Session, WorkspaceSummary } from "@tolkovo/api-client";
import { vi } from "vitest";

/** Test fixtures shaped by the generated API types, so a contract change breaks the tests. */
export const ownerSession: Session = {
  user: { id: "u-anna", email: "anna@zerno.test", name: "Анна Смирнова", locale: "ru" },
  workspace: { id: "w-zerno", slug: "zerno", name: "Зерно", locale: "ru", timezone: "Europe/Moscow" },
  role: "owner",
  permissions: ["members.manage", "members.view", "workspace.delete", "workspace.manage"],
  csrf_token: "csrf-owner",
};

export const operatorSession: Session = {
  ...ownerSession,
  user: { id: "u-petr", email: "petr@zerno.test", name: "Пётр Иванов", locale: "ru" },
  role: "operator",
  permissions: ["members.view"],
  csrf_token: "csrf-operator",
};

export const members: Member[] = [
  { user_id: "u-anna", email: "anna@zerno.test", name: "Анна Смирнова", role: "owner", active: true },
  { user_id: "u-petr", email: "petr@zerno.test", name: "Пётр Иванов", role: "operator", active: false },
];

export const workspaces: WorkspaceSummary[] = [{ id: "w-zerno", slug: "zerno", name: "Зерно", role: "owner" }];

type Handler = (request: Request) => Response | Promise<Response>;

export function json(status: number, body: unknown, headers: Record<string, string> = {}): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": status >= 400 ? "application/problem+json" : "application/json", ...headers },
  });
}

export function problem(status: number, code: string, headers: Record<string, string> = {}): Response {
  return json(status, { type: "about:blank", title: "Error", status, code }, headers);
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
    "GET /members": () => json(200, { items: members }),
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
      body: text ? (JSON.parse(text) as unknown) : undefined,
    });
    const handler = table[`${request.method} ${path}`];
    if (!handler) throw new Error(`fakeApi: no handler for ${request.method} ${path}`);
    return handler(request);
  });
  return { fetch, calls };
}
