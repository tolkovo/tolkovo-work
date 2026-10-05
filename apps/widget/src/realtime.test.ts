import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { Realtime, SUBPROTOCOL } from "./realtime";
import { FakeSocket } from "./test/fake";

function setup() {
  const h = { onRefetch: vi.fn(), onTyping: vi.fn(), onSessionEnded: vi.fn(), onStatus: vi.fn() };
  // random 0: the delay is exactly half the backoff step.
  const rt = new Realtime("wss://support.example.com/api/v1/widget/realtime", h, FakeSocket.factory, () => 0);
  return { rt, h };
}

describe("realtime", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    FakeSocket.all = [];
  });
  afterEach(() => vi.useRealTimers());

  it("opens with the subprotocol and the token, refetches on hello, replies and resyncs", () => {
    const { rt, h } = setup();
    rt.connect("tk_wsess_1");
    const ws = FakeSocket.last();
    expect(ws.protocols).toEqual([SUBPROTOCOL, "tk_wsess_1"]);
    ws.receive("hello", { heartbeat_interval_ms: 25000 });
    expect(h.onStatus).toHaveBeenLastCalledWith(true);
    ws.receive("widget.message", { contact_id: "c", conversation_id: "v", message_id: "m" });
    ws.receive("resync_required");
    ws.receive("heartbeat");
    ws.receive("widget.typing", { state: "start" });
    expect(h.onRefetch).toHaveBeenCalledTimes(3);
    expect(h.onTyping).toHaveBeenCalledTimes(1);
  });

  it("reconnects with a growing backoff that a hello resets", () => {
    const { rt, h } = setup();
    rt.connect("t");
    FakeSocket.last().drop(1006);
    expect(h.onStatus).toHaveBeenLastCalledWith(false);
    vi.advanceTimersByTime(249);
    expect(FakeSocket.all).toHaveLength(1);
    vi.advanceTimersByTime(1);
    expect(FakeSocket.all).toHaveLength(2);
    FakeSocket.last().drop(1012);
    vi.advanceTimersByTime(499);
    expect(FakeSocket.all).toHaveLength(2);
    vi.advanceTimersByTime(1);
    expect(FakeSocket.all).toHaveLength(3);
    FakeSocket.last().receive("hello", { heartbeat_interval_ms: 25000 });
    FakeSocket.last().drop(1006);
    vi.advanceTimersByTime(250);
    expect(FakeSocket.all).toHaveLength(4);
  });

  it("waits long after a policy close and does not reconnect when the session ended", () => {
    const { rt, h } = setup();
    rt.connect("t");
    FakeSocket.last().drop(4429);
    vi.advanceTimersByTime(14_999);
    expect(FakeSocket.all).toHaveLength(1);
    vi.advanceTimersByTime(1);
    expect(FakeSocket.all).toHaveLength(2);
    FakeSocket.last().drop(4401);
    expect(h.onSessionEnded).toHaveBeenCalledOnce();
    vi.advanceTimersByTime(60_000);
    expect(FakeSocket.all).toHaveLength(2);
  });

  it("drops a connection that hears nothing for two heartbeats", () => {
    const { rt } = setup();
    rt.connect("t");
    const ws = FakeSocket.last();
    ws.receive("hello", { heartbeat_interval_ms: 1000 });
    vi.advanceTimersByTime(1999);
    expect(ws.closed).toBeNull();
    vi.advanceTimersByTime(1);
    expect(ws.closed).toBe(4000);
    vi.advanceTimersByTime(250);
    expect(FakeSocket.all).toHaveLength(2);
  });

  it("stops for good", () => {
    const { rt } = setup();
    rt.connect("t");
    const ws = FakeSocket.last();
    rt.stop();
    expect(ws.closed).toBe(1000);
    vi.advanceTimersByTime(60_000);
    expect(FakeSocket.all).toHaveLength(1);
  });
});
