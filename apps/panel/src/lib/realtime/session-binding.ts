import type { ApiClient, Session } from "@tolkovo/api-client";
import { queryKeys } from "@tolkovo/api-client/react";
import { hashKey, type QueryClient } from "@tanstack/react-query";

import { resetDrafts } from "../inbox/drafts";
import { RealtimeConnection, type ConnectionOptions } from "./connection";
import { createInboxSync } from "./inbox-sync";
import { applySignal, resetSignals, signalSender, type SignalSender } from "./signals";

type Connection = Pick<RealtimeConnection, "start" | "stop" | "send">;

type BindOptions = {
  url: string;
  api: ApiClient;
  createConnection?: (options: ConnectionOptions) => Connection;
  sender?: SignalSender;
};

/**
 * Keeps one realtime connection per signed-in session (user + workspace). Domain events patch
 * the TanStack Query cache through the inbox sync, so the cache stays the only server state
 * (docs/00 §7.2); signals go to their own store; the operator's signals go out through it.
 * Returns a function that stops it.
 */
export function bindRealtime(
  queryClient: QueryClient,
  { url, api, createConnection, sender = signalSender }: BindOptions,
): () => void {
  const make = createConnection ?? ((options: ConnectionOptions) => new RealtimeConnection(options));
  let current: { key: string; connection: Connection; sync: ReturnType<typeof createInboxSync> } | null = null;

  const stopCurrent = () => {
    if (!current) return;
    current.connection.stop();
    current.sync.stop();
    sender.attach(null);
    resetSignals();
    resetDrafts();
    current = null;
  };

  const sync = () => {
    const session = queryClient.getQueryData<Session | null>(queryKeys.session);
    const key = session ? `${session.user.id}:${session.workspace.id}` : null;
    if (key === (current?.key ?? null)) return;
    stopCurrent();
    if (!key) return;
    const inbox = createInboxSync({ queryClient, api });
    const connection = make({
      url,
      handlers: {
        onEvent: (event) => inbox.onEvent(event),
        onResync: () => inbox.onResync(),
        // Re-check over REST: a 401 marks the user signed out and the shell redirects.
        onSessionEnded: () => void queryClient.invalidateQueries({ queryKey: queryKeys.session }),
        onSignal: (signal) => applySignal(signal),
        onReady: () => sender.ready(),
      },
    });
    current = { key, connection, sync: inbox };
    sender.attach(connection);
    connection.start();
  };

  const sessionHash = hashKey(queryKeys.session);
  const unsubscribe = queryClient.getQueryCache().subscribe((event) => {
    if (event.query.queryHash === sessionHash) sync();
  });
  sync();

  return () => {
    unsubscribe();
    stopCurrent();
  };
}
