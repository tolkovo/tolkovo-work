import { isApiError } from "@tolkovo/api-client";
import { queryKeys } from "@tolkovo/api-client/react";
import { MutationCache, QueryCache, QueryClient } from "@tanstack/react-query";

/**
 * The only home of server state (docs/00 §7.2). WebSocket events will patch this cache
 * directly instead of keeping a parallel store.
 */
export function createQueryClient(): QueryClient {
  const client: QueryClient = new QueryClient({
    // A request answered "unauthenticated" means the session ended (expired, revoked,
    // membership removed): mark the user signed out, and the shell sends them to sign-in.
    queryCache: new QueryCache({ onError: (error) => onUnauthenticated(client, error) }),
    mutationCache: new MutationCache({ onError: (error) => onUnauthenticated(client, error) }),
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
  return client;
}

function onUnauthenticated(client: QueryClient, error: unknown): void {
  if (isApiError(error) && error.status === 401 && error.code === "unauthenticated") {
    client.setQueryData(queryKeys.session, null);
  }
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
