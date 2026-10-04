import { describe, expect, it, vi } from "vitest";

import { CSRF_HEADER, createApiClient } from "./client";
import {
  changePassword,
  fetchMySessions,
  fetchSession,
  login,
  logout,
  revokeSession,
  updateWorkspace,
  type Session,
} from "./endpoints";
import { ApiError } from "./errors";

const BASE = "http://panel.test/api/v1";

const session: Session = {
  user: { id: "u1", email: "anna@example.com", name: "Anna", locale: "ru", email_verified: true },
  workspace: { id: "w1", slug: "zerno", name: "Zerno", locale: "ru", timezone: "Europe/Moscow" },
  role: "owner",
  permissions: ["members.view", "workspace.manage"],
  csrf_token: "csrf-1",
};

function json(status: number, body: unknown, headers: Record<string, string> = {}): Response {
  const type = status >= 400 ? "application/problem+json" : "application/json";
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": type, ...headers } });
}

function problem(status: number, code: string, headers: Record<string, string> = {}): Response {
  return json(status, { type: "about:blank", title: "x", status, code }, headers);
}

/** A client whose fetch records requests and answers with `respond`. */
function setup(respond: (request: Request) => Response | Promise<Response>, token: string | null = "csrf-1") {
  const requests: Request[] = [];
  const fetch = vi.fn(async (request: Request) => {
    requests.push(request.clone());
    return respond(request);
  });
  const client = createApiClient({ baseUrl: BASE, getCsrfToken: () => token ?? undefined, fetch });
  return { client, requests };
}

describe("CSRF middleware", () => {
  it("sends the session token on unsafe methods", async () => {
    const { client, requests } = setup((r) =>
      r.method === "PATCH" ? json(200, session.workspace) : new Response(null, { status: 204 }),
    );
    await logout(client);
    await updateWorkspace(client, { name: "Zerno 2" });
    expect(requests.map((r) => [r.method, r.headers.get(CSRF_HEADER)])).toEqual([
      ["POST", "csrf-1"],
      ["PATCH", "csrf-1"],
    ]);
  });

  it("never sends the token on safe methods", async () => {
    const { client, requests } = setup(() => json(200, session));
    await fetchSession(client);
    expect(requests[0]?.method).toBe("GET");
    expect(requests[0]?.headers.has(CSRF_HEADER)).toBe(false);
  });

  it("sends no header before sign-in", async () => {
    const { client, requests } = setup(() => json(200, session), null);
    await login(client, { email: "anna@example.com", password: "secret-pass" });
    expect(requests[0]?.headers.has(CSRF_HEADER)).toBe(false);
  });

  it("includes credentials so the session cookie goes along", async () => {
    const { client, requests } = setup(() => json(200, session));
    await fetchSession(client);
    expect(requests[0]?.credentials).toBe("include");
    expect(requests[0]?.url).toBe(`${BASE}/me`);
  });
});

describe("errors", () => {
  it("treats 401 on GET /me as signed out", async () => {
    const { client } = setup(() => problem(401, "unauthenticated"));
    await expect(fetchSession(client)).resolves.toBeNull();
  });

  it("carries the problem code and Retry-After", async () => {
    const { client } = setup(() => problem(429, "too_many_requests", { "Retry-After": "42" }));
    const error = await login(client, { email: "a@b.c", password: "x" }).catch((e: unknown) => e);
    expect(error).toBeInstanceOf(ApiError);
    expect(error).toMatchObject({ status: 429, code: "too_many_requests", retryAfter: 42 });
  });

  it("reports a request that never reached the server as a network error", async () => {
    const { client } = setup(() => Promise.reject(new TypeError("Failed to fetch")));
    await expect(fetchSession(client)).rejects.toMatchObject({ status: 0, code: "network" });
  });

  it("rejects an answer that breaks the contract", async () => {
    const { client } = setup(() => json(200, { ...session, csrf_token: undefined }));
    await expect(fetchSession(client)).rejects.toMatchObject({ code: "invalid_response" });
  });

  it("marks a non-problem error body as unknown", async () => {
    const { client } = setup(() => new Response("<html>Bad gateway</html>", { status: 502 }));
    await expect(fetchSession(client)).rejects.toMatchObject({ status: 502, code: "unknown" });
  });
});

describe("personal settings", () => {
  it("revokes a session by its id in the path, with the CSRF token", async () => {
    const { client, requests } = setup(() => new Response(null, { status: 204 }));
    await revokeSession(client, "0b6f4a6e-1c55-4d43-9d8e-4bd1f1f2a9a1");
    expect(requests[0]?.method).toBe("DELETE");
    expect(requests[0]?.url).toBe(`${BASE}/me/sessions/0b6f4a6e-1c55-4d43-9d8e-4bd1f1f2a9a1`);
    expect(requests[0]?.headers.get(CSRF_HEADER)).toBe("csrf-1");
  });

  it("returns the rotated session after a password change", async () => {
    const { client } = setup(() => json(200, { ...session, csrf_token: "csrf-2" }));
    const next = await changePassword(client, { current_password: "old-pass", new_password: "new-pass-1" });
    expect(next.csrf_token).toBe("csrf-2");
  });

  it("checks the session list against the contract", async () => {
    const item = {
      id: "s1",
      created_at: "2026-10-04T10:00:00Z",
      last_seen_at: "2026-10-04T11:00:00Z",
      user_agent: "",
      current: true,
    };
    const { client } = setup(() => json(200, { items: [item] }));
    await expect(fetchMySessions(client)).resolves.toEqual([item]);
    const broken = setup(() => json(200, { items: [{ ...item, current: "yes" }] }));
    await expect(fetchMySessions(broken.client)).rejects.toMatchObject({ code: "invalid_response" });
  });
});
