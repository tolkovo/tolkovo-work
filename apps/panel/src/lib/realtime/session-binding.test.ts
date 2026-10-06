import type { ApiClient, Session } from "@tolkovo/api-client";
import { queryKeys } from "@tolkovo/api-client/react";
import { QueryClient } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { ConnectionOptions } from "./connection";
import { bindRealtime } from "./session-binding";
import { SignalSender, useSignalStore } from "./signals";

function session(userId: string, workspaceId: string): Session {
  return {
    user: { id: userId },
    workspace: { id: workspaceId },
    role: "operator",
    permissions: [],
    csrf_token: "t",
  } as unknown as Session;
}

type FakeConnection = {
  options: ConnectionOptions;
  start: ReturnType<typeof vi.fn>;
  stop: ReturnType<typeof vi.fn>;
  send: ReturnType<typeof vi.fn>;
};

function setup() {
  const queryClient = new QueryClient();
  const connections: FakeConnection[] = [];
  const sender = new SignalSender({ setInterval: () => 0, clearInterval: () => undefined });
  const unbind = bindRealtime(queryClient, {
    url: "ws://panel.test/api/v1/realtime",
    api: {} as ApiClient,
    sender,
    createConnection: (options) => {
      const c = { options, start: vi.fn(), stop: vi.fn(), send: vi.fn(() => true) };
      connections.push(c);
      return c;
    },
  });
  const current = () => {
    const c = connections.at(-1);
    if (!c) throw new Error("no connection");
    return c;
  };
  return { queryClient, connections, current, unbind, sender };
}

describe("bindRealtime", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it("connects only while signed in and reconnects on a workspace switch", () => {
    const { queryClient, connections, unbind } = setup();
    expect(connections).toHaveLength(0);

    queryClient.setQueryData(queryKeys.session, session("u1", "w1"));
    expect(connections).toHaveLength(1);
    expect(connections[0]?.start).toHaveBeenCalledTimes(1);

    queryClient.setQueryData(queryKeys.session, session("u1", "w1")); // same session: no reconnect
    expect(connections).toHaveLength(1);

    queryClient.setQueryData(queryKeys.session, session("u1", "w2"));
    expect(connections[0]?.stop).toHaveBeenCalledTimes(1);
    expect(connections).toHaveLength(2);

    queryClient.setQueryData(queryKeys.session, null);
    expect(connections[1]?.stop).toHaveBeenCalledTimes(1);
    expect(connections).toHaveLength(2);
    unbind();
  });

  it("refetches realtime-backed queries on resync, in one batch", async () => {
    const { queryClient, current, unbind } = setup();
    queryClient.setQueryData(queryKeys.session, session("u1", "w1"));
    const invalidate = vi.spyOn(queryClient, "invalidateQueries");
    current().options.handlers.onResync();
    current().options.handlers.onResync();
    expect(invalidate).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(50);
    expect(invalidate.mock.calls.map(([filters]) => filters?.queryKey)).toEqual([
      queryKeys.conversations,
      queryKeys.contacts,
      queryKeys.tasks,
    ]);
    unbind();
  });

  it("stores signals, sends the open conversation again on reconnect and forgets both with the session", () => {
    const { queryClient, current, sender, unbind } = setup();
    queryClient.setQueryData(queryKeys.session, session("u1", "w1"));
    const { handlers } = current().options;
    handlers.onSignal?.({
      type: "conversation.viewers",
      id: "v",
      ts: "2026-10-04T12:00:00Z",
      data: { conversation_id: "c1", user_ids: ["u2"] },
    });
    expect(useSignalStore.getState().viewers.c1).toEqual(["u2"]);

    sender.view("c1");
    expect(current().send).toHaveBeenCalledTimes(1);
    handlers.onReady?.();
    expect(current().send).toHaveBeenCalledTimes(2);
    expect(JSON.parse(current().send.mock.calls[1]?.[0] as string)).toMatchObject({
      type: "viewing",
      data: { conversation_id: "c1", state: "start" },
    });

    queryClient.setQueryData(queryKeys.session, null);
    expect(useSignalStore.getState().viewers).toEqual({});
    sender.view(null);
    unbind();
  });

  it("re-checks the session over REST when the server ends it", () => {
    const { queryClient, current, unbind } = setup();
    queryClient.setQueryData(queryKeys.session, session("u1", "w1"));
    const invalidate = vi.spyOn(queryClient, "invalidateQueries");
    current().options.handlers.onSessionEnded();
    expect(invalidate).toHaveBeenCalledWith({ queryKey: queryKeys.session });
    unbind();
  });

  it("stops the connection when unbound", () => {
    const { queryClient, current, unbind } = setup();
    queryClient.setQueryData(queryKeys.session, session("u1", "w1"));
    unbind();
    expect(current().stop).toHaveBeenCalledTimes(1);
  });
});
