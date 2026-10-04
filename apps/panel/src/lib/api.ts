import { createApiClient, type ApiClient } from "@tolkovo/api-client";
import { csrfTokenFrom } from "@tolkovo/api-client/react";
import type { QueryClient } from "@tanstack/react-query";

/**
 * The panel's REST client (docs/00 §7.1, §8): types and runtime from packages/api-client.
 * The API lives on the panel's own origin, so the session cookie is same-origin; in
 * development Vite proxies /api to the Go server on http://localhost:8080 (vite.config.ts).
 */
export const API_BASE_PATH = "/api/v1";

export function createPanelApiClient(
  queryClient: QueryClient,
  { origin = window.location.origin, fetch }: { origin?: string; fetch?: (request: Request) => Promise<Response> } = {},
): ApiClient {
  return createApiClient({
    baseUrl: new URL(API_BASE_PATH, origin).href,
    // The CSRF token lives in the cached session (server state stays in TanStack Query).
    getCsrfToken: () => csrfTokenFrom(queryClient),
    ...(fetch ? { fetch } : {}),
  });
}
