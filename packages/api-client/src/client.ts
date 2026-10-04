import createClient, { type Client, type Middleware } from "openapi-fetch";
import type { z } from "zod";

import { ApiError, apiErrorFromResponse } from "./errors";
import type { paths } from "./schema.gen";

/** Header that carries the session CSRF token (api/openapi.yaml, "CSRF"; docs/02 §13.1). */
export const CSRF_HEADER = "X-CSRF-Token";

const UNSAFE_METHODS = new Set(["POST", "PUT", "PATCH", "DELETE"]);

export type ApiClient = Client<paths>;

export type ApiClientOptions = {
  /** Absolute URL of `/api/v1` on the panel's own origin (cookies are same-origin). */
  baseUrl: string;
  /** Current session's `csrf_token`, or undefined before sign-in. */
  getCsrfToken: () => string | undefined;
  /** Custom fetch for tests; defaults to the global fetch. */
  fetch?: (request: Request) => Promise<Response>;
};

/**
 * Adds the session CSRF token to every unsafe request. Safe methods never carry it, so the
 * token does not end up in logs of GET requests. Sign-in requests go out without a token
 * (there is no session yet); the server protects them with the Origin check.
 */
export function csrfMiddleware(getCsrfToken: () => string | undefined): Middleware {
  return {
    onRequest({ request }) {
      if (!UNSAFE_METHODS.has(request.method.toUpperCase())) return undefined;
      const token = getCsrfToken();
      if (token) request.headers.set(CSRF_HEADER, token);
      return request;
    },
  };
}

/** The typed REST client: session cookie on every request, CSRF token on unsafe ones. */
export function createApiClient({ baseUrl, getCsrfToken, fetch }: ApiClientOptions): ApiClient {
  const client = createClient<paths>({
    baseUrl,
    credentials: "include",
    ...(fetch ? { fetch } : {}),
  });
  client.use(csrfMiddleware(getCsrfToken));
  return client;
}

type Result<T> =
  | { data: T; error?: never; response: Response }
  | { data?: never; error: unknown; response: Response };

/**
 * Awaits an openapi-fetch call and returns its data, or throws ApiError: with the server's
 * problem code for an error status, `network` when the server was not reached.
 */
export async function unwrap<T>(pending: Promise<Result<T>>): Promise<T> {
  let result: Result<T>;
  try {
    result = await pending;
  } catch (cause) {
    throw new ApiError(0, "network", { cause });
  }
  if (!result.response.ok) throw apiErrorFromResponse(result.response, result.error);
  return result.data as T;
}

/** Checks an answer against its schema; a mismatch is `invalid_response`, not a crash later. */
export function parseAnswer<S extends z.ZodType>(schema: S, data: unknown, status = 200): z.output<S> {
  const parsed = schema.safeParse(data);
  if (!parsed.success) throw new ApiError(status, "invalid_response", { cause: parsed.error });
  return parsed.data;
}
