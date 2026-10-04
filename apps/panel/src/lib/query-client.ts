import { QueryClient } from "@tanstack/react-query";

/**
 * The only home of server state (docs/00 §7.2). WebSocket events will patch this cache
 * directly instead of keeping a parallel store.
 */
export function createQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: {
        staleTime: 30_000,
        gcTime: 5 * 60_000,
        // 4xx are answers, not outages: do not hammer the API with retries.
        retry: (failureCount, error) => !isClientError(error) && failureCount < 2,
        refetchOnWindowFocus: true,
      },
      mutations: {
        retry: false,
      },
    },
  });
}

function isClientError(error: unknown): boolean {
  return (
    typeof error === "object" &&
    error !== null &&
    "status" in error &&
    typeof error.status === "number" &&
    error.status >= 400 &&
    error.status < 500
  );
}
