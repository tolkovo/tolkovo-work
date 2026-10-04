import { problemSchema } from "./schemas";

/**
 * Machine-readable error codes the panel reacts to. `code` of the server's problem+json
 * (api/openapi.yaml, docs/00 §8), plus three client-side codes:
 * - `network` — the request did not reach the server (offline, DNS, server down);
 * - `invalid_response` — the answer does not match the contract;
 * - `unknown` — an error body that is not problem+json (e.g. a proxy page).
 */
export type ApiErrorCode = "network" | "invalid_response" | "unknown" | (string & {});

/** A failed API call. `status` is 0 when the server was not reached. */
export class ApiError extends Error {
  readonly status: number;
  readonly code: ApiErrorCode;
  /** Seconds from `Retry-After` (429), otherwise null. */
  readonly retryAfter: number | null;
  /** Server request id, to quote in a bug report. */
  readonly requestId: string | null;

  constructor(
    status: number,
    code: ApiErrorCode,
    options: { retryAfter?: number | null; requestId?: string | null; cause?: unknown } = {},
  ) {
    super(`API error ${status}: ${code}`, { cause: options.cause });
    this.name = "ApiError";
    this.status = status;
    this.code = code;
    this.retryAfter = options.retryAfter ?? null;
    this.requestId = options.requestId ?? null;
  }
}

export function isApiError(error: unknown): error is ApiError {
  return error instanceof ApiError;
}

/** Builds an ApiError from an error response and its parsed body (openapi-fetch `error`). */
export function apiErrorFromResponse(response: Response, body: unknown): ApiError {
  const problem = problemSchema.safeParse(body);
  return new ApiError(response.status, problem.success ? problem.data.code : "unknown", {
    retryAfter: parseRetryAfter(response.headers.get("Retry-After")),
    requestId: problem.success ? (problem.data.request_id ?? null) : null,
  });
}

/** `Retry-After` in seconds; the HTTP-date form is not used by the Tolkovo server. */
function parseRetryAfter(value: string | null): number | null {
  if (value === null || !/^\d+$/.test(value.trim())) return null;
  return Number(value.trim());
}
