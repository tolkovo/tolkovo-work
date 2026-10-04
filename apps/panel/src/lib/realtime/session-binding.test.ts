import type { Session } from "@tolkovo/api-client";
import { queryKeys } from "@tolkovo/api-client/react";
import { QueryClient } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { ConnectionOptions } from "./connection";
import { bindRealtime, queryKeysForEvent } from "./session-binding";

function session(userId: string, workspaceId: string): Session {
  return {
    user: { id: userId },
    workspace: { id: workspaceId },
    role: "operator",
    permissions: [],
    csrf_token: "t",
  } as unknown as Session;
}

function setup() {
  const queryClient = new QueryClient();
  const connections: { options: ConnectionOptions; start: ReturnType<typeof vi.fn>; stop: ReturnType<typeof vi.fn> }[] =
    [];
  const unbind = bindRealtime(queryClient, {
    url: "ws://panel.test/api/v1/realtime",
    createConnection: (options) => {
      const c = { options, start: vi.fn(), stop: vi.fn() };
      connections.push(c);
      return c;
    },
  });
  const current = () => {
    const c = connections.at(-1);
    if (!c) throw new Error("no connection");
    return c;
  };
  return { queryClient, connections, current, unbind };
}

const event = (type: string) => ({ type, id: "1", ts: "2026-10-04T12:00:00Z", seq: 1, data: {} });

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

  it("invalidates conversation and contact queries in one batch per burst", async () => {
    const { queryClient, current, unbind } = setup();
    queryClient.setQueryData(queryKeys.session, session("u1", "w1"));
    const invalidate = vi.spyOn(queryClient, "invalidateQueries");

    const { handlers } = current().options;
    handlers.onEvent(event("message.created"));
    handlers.onEvent(event("conversation.assigned"));
    handlers.onEvent(event("contact.updated"));
    expect(invalidate).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(50);
    expect(invalidate.mock.calls.map(([filters]) => filters?.queryKey)).toEqual([
      queryKeys.conversations,
      queryKeys.contacts,
    ]);

    invalidate.mockClear();
    handlers.onResync();
    await vi.advanceTimersByTimeAsync(50);
    expect(invalidate).toHaveBeenCalledTimes(2);
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

describe("queryKeysForEvent", () => {
  it.each([
    ["conversation.created", [queryKeys.conversations]],
    ["message.created", [queryKeys.conversations]],
    ["contact.updated", [queryKeys.contacts, queryKeys.conversations]],
    ["sla.breached", []],
  ])("%s", (type, keys) => {
    expect(queryKeysForEvent(type)).toEqual(keys);
  });
});
