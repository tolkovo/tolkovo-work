import type { Session } from "@tolkovo/api-client";
import { queryKeys } from "@tolkovo/api-client/react";
import { hashKey, type QueryClient, type QueryKey } from "@tanstack/react-query";

import { RealtimeConnection, type ConnectionOptions } from "./connection";

/** Query roots a domain event makes stale. Conversations embed their contact, hence both. */
export function queryKeysForEvent(type: string): QueryKey[] {
  if (type.startsWith("conversation.") || type.startsWith("message.")) return [queryKeys.conversations];
  if (type.startsWith("contact.")) return [queryKeys.contacts, queryKeys.conversations];
  return [];
}

const REALTIME_ROOTS: QueryKey[] = [queryKeys.conversations, queryKeys.contacts];

/** A burst of events (a replay, a bulk action) becomes one refetch per query. */
const INVALIDATE_BATCH_MS = 50;

type BindOptions = {
  url: string;
  createConnection?: (options: ConnectionOptions) => Pick<RealtimeConnection, "start" | "stop">;
};

/**
 * Keeps one realtime connection per signed-in session (user + workspace) and turns events into
 * TanStack Query invalidations: the cache stays the only server state (docs/00 §7.2). Events
 * only invalidate for now; patching the cache in place comes with the inbox (I-01).
 * Returns a function that stops it.
 */
export function bindRealtime(queryClient: QueryClient, { url, createConnection }: BindOptions): () => void {
  const make = createConnection ?? ((options: ConnectionOptions) => new RealtimeConnection(options));
  let current: { key: string; connection: Pick<RealtimeConnection, "start" | "stop"> } | null = null;

  const pending = new Map<string, QueryKey>();
  let flushTimer: ReturnType<typeof setTimeout> | undefined;
  const invalidate = (keys: QueryKey[]) => {
    for (const key of keys) pending.set(JSON.stringify(key), key);
    if (flushTimer !== undefined || pending.size === 0) return;
    flushTimer = setTimeout(() => {
      flushTimer = undefined;
      const keys = [...pending.values()];
      pending.clear();
      for (const queryKey of keys) void queryClient.invalidateQueries({ queryKey });
    }, INVALIDATE_BATCH_MS);
  };

  const sync = () => {
    const session = queryClient.getQueryData<Session | null>(queryKeys.session);
    const key = session ? `${session.user.id}:${session.workspace.id}` : null;
    if (key === (current?.key ?? null)) return;
    current?.connection.stop();
    current = null;
    if (!key) return;
    const connection = make({
      url,
      handlers: {
        onEvent: (event) => invalidate(queryKeysForEvent(event.type)),
        onResync: () => invalidate(REALTIME_ROOTS),
        // Re-check over REST: a 401 marks the user signed out and the shell redirects.
        onSessionEnded: () => void queryClient.invalidateQueries({ queryKey: queryKeys.session }),
      },
    });
    current = { key, connection };
    connection.start();
  };

  const sessionHash = hashKey(queryKeys.session);
  const unsubscribe = queryClient.getQueryCache().subscribe((event) => {
    if (event.query.queryHash === sessionHash) sync();
  });
  sync();

  return () => {
    unsubscribe();
    clearTimeout(flushTimer);
    current?.connection.stop();
    current = null;
  };
}
