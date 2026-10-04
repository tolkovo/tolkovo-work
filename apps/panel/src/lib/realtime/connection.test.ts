import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { RealtimeConnection, type RealtimeHandlers, type WebSocketLike } from "./connection";

/** A socket the test drives from the server side. */
class FakeSocket implements WebSocketLike {
  sent: string[] = [];
  closedWith: number | undefined;
  onopen: WebSocketLike["onopen"] = null;
  onmessage: WebSocketLike["onmessage"] = null;
  onclose: WebSocketLike["onclose"] = null;
  onerror: WebSocketLike["onerror"] = null;

  constructor(readonly url: string) {}

  send(data: string) {
    this.sent.push(data);
  }
  close(code?: number) {
    this.closedWith = code;
  }
  // Server side.
  open() {
    this.onopen?.call(this as unknown as WebSocket, new Event("open"));
  }
  push(message: unknown) {
    this.onmessage?.call(this as unknown as WebSocket, new MessageEvent("message", { data: JSON.stringify(message) }));
  }
  drop(code: number) {
    this.onclose?.call(this as unknown as WebSocket, new CloseEvent("close", { code }));
  }
}

const ts = "2026-10-04T12:00:00Z";
const hello = (seq: number) => ({ type: "hello", id: `h${seq}`, ts, data: { seq, heartbeat_interval_ms: 25_000 } });
const event = (id: string, seq: number, type = "conversation.created") => ({ type, id, ts, seq, data: {} });

function setup({ random = (): number => 0.5 }: { random?: () => number } = {}) {
  const sockets: FakeSocket[] = [];
  const handlers: RealtimeHandlers = { onEvent: vi.fn(), onResync: vi.fn(), onSessionEnded: vi.fn() };
  const connection = new RealtimeConnection({
    url: "ws://panel.test/api/v1/realtime",
    handlers,
    random,
    newId: () => "rid",
    now: () => new Date(ts),
    createSocket: (url) => {
      const s = new FakeSocket(url);
      sockets.push(s);
      return s;
    },
  });
  const last = () => {
    const s = sockets.at(-1);
    if (!s) throw new Error("no socket");
    return s;
  };
  return { sockets, handlers, connection, last };
}

describe("RealtimeConnection", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it("starts from hello without history, then delivers each event once", () => {
    const { connection, handlers, last } = setup();
    connection.start();
    last().open();
    last().push(hello(10));
    expect(handlers.onResync).toHaveBeenCalledTimes(1);
    expect(last().sent).toEqual([]); // nothing to resume

    last().push(event("1", 11));
    last().push(event("1", 11)); // a redelivery
    last().push({ type: "heartbeat", id: "x", ts, data: {} });
    last().push({ garbage: true });
    expect(handlers.onEvent).toHaveBeenCalledTimes(1);
    connection.stop();
  });

  it("reconnects with backoff and resumes from the last seq", () => {
    const { connection, handlers, sockets, last } = setup({ random: () => 0 });
    connection.start();
    last().push(hello(10));
    last().push(event("1", 12));
    last().drop(1006);

    // Equal jitter with random()=0: half the base delay (500 ms) for the first attempt.
    vi.advanceTimersByTime(249);
    expect(sockets).toHaveLength(1);
    vi.advanceTimersByTime(1);
    expect(sockets).toHaveLength(2);

    last().push(hello(15));
    expect(JSON.parse(last().sent[0] ?? "{}")).toMatchObject({ type: "resume", data: { last_seq: 12 } });
    last().push(event("2", 13));
    last().push({ type: "resumed", id: "r", ts, data: { seq: 13 } });
    expect(handlers.onEvent).toHaveBeenCalledTimes(2);
    expect(handlers.onResync).toHaveBeenCalledTimes(1); // only the very first hello
    connection.stop();
  });

  it("doubles the delay per failed attempt and resets it after hello", () => {
    const { connection, sockets, last } = setup({ random: () => 1 });
    connection.start();
    last().drop(1006);
    vi.advanceTimersByTime(500);
    expect(sockets).toHaveLength(2);
    last().drop(1006);
    vi.advanceTimersByTime(999);
    expect(sockets).toHaveLength(2);
    vi.advanceTimersByTime(1);
    expect(sockets).toHaveLength(3);
    last().push(hello(1));
    last().drop(1012); // server restart
    vi.advanceTimersByTime(500);
    expect(sockets).toHaveLength(4);
    connection.stop();
  });

  it("keeps the resume point before a gap when live events overtake the replay", () => {
    const { connection, last } = setup();
    connection.start();
    last().push(hello(10));
    last().drop(1006);
    vi.advanceTimersByTime(1000);
    last().push(hello(30));
    last().push(event("live", 30)); // arrived before the replay of 11..29
    last().drop(1006); // gone before resumed
    vi.advanceTimersByTime(2000);
    last().push(hello(30));
    expect(JSON.parse(last().sent[0] ?? "{}")).toMatchObject({ data: { last_seq: 10 } });
    connection.stop();
  });

  it("asks for a refetch on resync_required", () => {
    const { connection, handlers, last } = setup();
    connection.start();
    last().push(hello(10));
    last().push({ type: "resync_required", id: "r", ts, data: { seq: 500 } });
    expect(handlers.onResync).toHaveBeenCalledTimes(2);
    last().drop(1006);
    vi.advanceTimersByTime(1000);
    last().push(hello(500));
    expect(JSON.parse(last().sent[0] ?? "{}")).toMatchObject({ data: { last_seq: 500 } });
    connection.stop();
  });

  it("stops for good when the session ends", () => {
    const { connection, handlers, sockets, last } = setup();
    connection.start();
    last().push(hello(1));
    last().drop(4401);
    vi.advanceTimersByTime(60_000);
    expect(handlers.onSessionEnded).toHaveBeenCalledTimes(1);
    expect(sockets).toHaveLength(1);
  });

  it.each([4429, 1008])("backs off to the maximum after close %i", (code) => {
    const { connection, sockets, last } = setup({ random: () => 0 });
    connection.start();
    last().push(hello(1));
    last().drop(code);
    vi.advanceTimersByTime(14_999);
    expect(sockets).toHaveLength(1);
    vi.advanceTimersByTime(1);
    expect(sockets).toHaveLength(2);
    connection.stop();
  });

  it("reconnects when heartbeats stop", () => {
    const { connection, sockets, last } = setup({ random: () => 0 });
    connection.start();
    last().push(hello(1));
    vi.advanceTimersByTime(49_999);
    expect(last().closedWith).toBeUndefined();
    vi.advanceTimersByTime(1);
    expect(sockets[0]?.closedWith).toBe(1000);
    vi.advanceTimersByTime(250);
    expect(sockets).toHaveLength(2);
    connection.stop();
  });

  it("stop closes the socket and cancels a pending reconnect", () => {
    const { connection, sockets, last } = setup();
    connection.start();
    last().drop(1006);
    connection.stop();
    vi.advanceTimersByTime(60_000);
    expect(sockets).toHaveLength(1);

    connection.start();
    const s = last();
    connection.stop();
    expect(s.closedWith).toBe(1000);
    s.drop(1000); // late close event of a detached socket
    vi.advanceTimersByTime(60_000);
    expect(sockets).toHaveLength(2);
  });
});
