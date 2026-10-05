import type { components } from "@tolkovo/api-client";

/**
 * The widget's REST calls (api/openapi.yaml, tag widget). Plain fetch: the panel's client and its
 * validation would cost more than the whole widget. Types come from the generated contract.
 */

type Schemas = components["schemas"];
export type WidgetSession = Schemas["WidgetSession"];
export type WidgetMessage = Schemas["WidgetMessage"];
export type WidgetMessages = Schemas["WidgetMessages"];
type WidgetSessionRequest = Schemas["WidgetSessionRequest"];
type WidgetReceiptRequest = Schemas["WidgetReceiptRequest"];

/** An answer that is not 2xx: the problem's code, and Retry-After for 429. */
export class WidgetApiError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    readonly retryAfter: number | null,
  ) {
    super(`tolkovo widget: ${status} ${code}`);
  }
}

/** The session ended (expired, ended, the widget disconnected): start a new one. */
export const isSessionEnded = (e: unknown) => e instanceof WidgetApiError && e.code === "widget_session_ended";

export class WidgetApi {
  private readonly base: string;

  constructor(apiBase: string) {
    this.base = apiBase.replace(/\/+$/, "") + "/api/v1/widget";
  }

  /** wss://host/api/v1/widget/realtime */
  realtimeUrl(): string {
    return this.base.replace(/^http/, "ws") + "/realtime";
  }

  private async call<T>(method: string, path: string, token: string | null, body?: unknown, headers: Record<string, string> = {}): Promise<T> {
    const init: RequestInit = { method, headers: { ...headers }, credentials: "omit", cache: "no-store" };
    const h = init.headers as Record<string, string>;
    if (token) h.Authorization = `Bearer ${token}`;
    if (body !== undefined) {
      h["Content-Type"] = "application/json";
      init.body = JSON.stringify(body);
    }
    const res = await fetch(this.base + path, init);
    if (!res.ok) {
      let code = "http_" + res.status;
      try {
        const problem = (await res.json()) as { code?: unknown };
        if (typeof problem.code === "string") code = problem.code;
      } catch {
        // Not a problem document (a proxy's error page): keep the status.
      }
      const retry = Number(res.headers.get("Retry-After"));
      throw new WidgetApiError(res.status, code, Number.isFinite(retry) && retry > 0 ? retry : null);
    }
    if (res.status === 204) return undefined as T;
    return (await res.json()) as T;
  }

  startSession(req: WidgetSessionRequest): Promise<WidgetSession> {
    return this.call("POST", "/session", null, req);
  }

  endSession(token: string): Promise<void> {
    return this.call("DELETE", "/session", token);
  }

  messages(token: string, since: string): Promise<WidgetMessages> {
    return this.call("GET", "/messages" + (since ? "?since=" + encodeURIComponent(since) : ""), token);
  }

  send(token: string, key: string, message: string): Promise<WidgetMessage> {
    return this.call("POST", "/messages", token, { message }, { "Idempotency-Key": key });
  }

  receipt(token: string, req: WidgetReceiptRequest): Promise<void> {
    return this.call("POST", "/receipts", token, req);
  }
}
