import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  SignalSender,
  TYPING_THROTTLE_MS,
  TYPING_TTL_MS,
  VIEWERS_TTL_MS,
  VIEWING_REFRESH_MS,
  applySignal,
  resetSignals,
  useTypists,
  useViewers,
} from "./signals";

const ts = "2026-10-04T12:00:00Z";
const viewers = (ids: string[]) =>
  ({ type: "conversation.viewers", id: "v", ts, data: { conversation_id: "c1", user_ids: ids } }) as const;
const typing = (actorId: string, state: "start" | "stop", actor: "operator" | "contact" = "operator") =>
  ({ type: "conversation.typing", id: "t", ts, data: { conversation_id: "c1", actor, actor_id: actorId, state } }) as const;

describe("signal store", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    resetSignals();
  });
  afterEach(() => vi.useRealTimers());

  it("lists other viewers until the list goes stale", () => {
    act(() => applySignal(viewers(["me", "u2"])));
    const { result } = renderHook(() => useViewers("c1", "me"));
    expect(result.current).toEqual(["u2"]);
    act(() => applySignal(viewers(["me"])));
    expect(result.current).toEqual([]);
    act(() => applySignal(viewers(["u3"])));
    act(() => void vi.advanceTimersByTime(VIEWERS_TTL_MS + 50));
    expect(result.current).toEqual([]);
  });

  it("shows typing until stop or the TTL, never my own", () => {
    const { result } = renderHook(() => useTypists("c1", "me"));
    act(() => {
      applySignal(typing("u2", "start"));
      applySignal(typing("me", "start"));
      applySignal(typing("k1", "start", "contact"));
    });
    expect(result.current).toEqual([
      { actor: "operator", actorId: "u2" },
      { actor: "contact", actorId: "k1" },
    ]);
    act(() => applySignal(typing("u2", "stop")));
    expect(result.current).toEqual([{ actor: "contact", actorId: "k1" }]);
    act(() => void vi.advanceTimersByTime(TYPING_TTL_MS + 50));
    expect(result.current).toEqual([]);
  });
});

describe("SignalSender", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  const sent = (send: ReturnType<typeof vi.fn>) =>
    send.mock.calls.map(([m]) => {
      const { type, data } = JSON.parse(m as string) as { type: string; data: { conversation_id: string; state: string } };
      return `${type}:${data.conversation_id}:${data.state}`;
    });

  it("announces the open conversation, refreshes it and stops it", () => {
    const send = vi.fn(() => true);
    const sender = new SignalSender();
    sender.attach({ send });
    sender.view("c1");
    vi.advanceTimersByTime(VIEWING_REFRESH_MS);
    sender.view("c2"); // the server replaces c1 by itself
    sender.view(null);
    vi.advanceTimersByTime(VIEWING_REFRESH_MS * 2);
    expect(sent(send)).toEqual(["viewing:c1:start", "viewing:c1:start", "viewing:c2:start", "viewing:c2:stop"]);
  });

  it("throttles typing start and sends stop once", () => {
    let now = Date.parse(ts);
    const send = vi.fn(() => true);
    const sender = new SignalSender({ now: () => new Date(now) });
    sender.attach({ send });
    sender.typing("c1");
    now += 1000;
    sender.typing("c1");
    now += TYPING_THROTTLE_MS;
    sender.typing("c1");
    sender.stopTyping("c1");
    sender.stopTyping("c1");
    expect(sent(send)).toEqual(["typing:c1:start", "typing:c1:start", "typing:c1:stop"]);
  });

  it("does nothing without a connection and resends viewing when one is ready", () => {
    const sender = new SignalSender();
    sender.view("c1");
    const send = vi.fn(() => true);
    sender.attach({ send });
    sender.ready();
    expect(sent(send)).toEqual(["viewing:c1:start"]);
    sender.view(null);
  });
});
