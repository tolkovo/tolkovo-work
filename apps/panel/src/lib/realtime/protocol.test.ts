import { describe, expect, it } from "vitest";

import clientSchema from "../../../../../api/ws/client-message.schema.json";
import serverSchema from "../../../../../api/ws/server-message.schema.json";
import {
  CLOSE_CODES,
  CONTROL_TYPES,
  EVENT_TYPES,
  SIGNAL_TYPES,
  isEvent,
  isSignal,
  parseServerMessage,
  realtimeUrl,
  resumeMessage,
  signalMessage,
} from "./protocol";

// api/ws is the source of truth; this file mirrors it by hand until WS types are generated.
describe("realtime protocol mirrors api/ws", () => {
  it("knows every message type and close code of the schema", () => {
    expect([...CONTROL_TYPES, ...SIGNAL_TYPES, ...EVENT_TYPES].sort()).toEqual(
      [...serverSchema.$defs.messageType.enum].sort(),
    );
    expect(Object.values(CLOSE_CODES).sort()).toEqual([...serverSchema.$defs.closeCode.enum].sort());
  });

  it("builds resume exactly as the client schema describes it", () => {
    const resume = JSON.parse(resumeMessage(42, "r1", new Date("2026-10-04T12:00:00Z"))) as Record<string, unknown>;
    const def = clientSchema.$defs.resume;
    expect(Object.keys(resume).sort()).toEqual([...def.required].sort());
    expect(resume).toEqual({ type: "resume", id: "r1", ts: "2026-10-04T12:00:00.000Z", data: { last_seq: 42 } });
  });

  it.each(["typing", "viewing"] as const)("builds %s as the client schema describes it", (type) => {
    const message = JSON.parse(signalMessage(type, "c1", "start", "s1", new Date("2026-10-04T12:00:00Z"))) as Record<
      string,
      unknown
    >;
    const def = clientSchema.$defs[type];
    expect(Object.keys(message).sort()).toEqual([...def.required].sort());
    expect(Object.keys(message.data as object).sort()).toEqual([...clientSchema.$defs.signalData.required].sort());
    expect(message).toEqual({
      type,
      id: "s1",
      ts: "2026-10-04T12:00:00.000Z",
      data: { conversation_id: "c1", state: "start" },
    });
  });

  it("marks a typing start as a reply with a mode the client schema knows", () => {
    const message = JSON.parse(signalMessage("typing", "c1", "start", "s1", new Date(0), "reply")) as {
      data: Record<string, unknown>;
    };
    expect(message.data).toEqual({ conversation_id: "c1", state: "start", mode: "reply" });
    expect(clientSchema.$defs.typingData.properties.mode.enum).toContain(message.data.mode);
  });
});

describe("parseServerMessage", () => {
  const ts = "2026-10-04T12:00:00Z";

  it("parses control messages and events", () => {
    const hello = parseServerMessage(
      JSON.stringify({ type: "hello", id: "h", ts, data: { seq: 5, heartbeat_interval_ms: 25000 } }),
    );
    expect(hello?.type).toBe("hello");
    const event = parseServerMessage(
      JSON.stringify({ type: "contact.created", id: "7", ts, seq: 8, data: { contact_id: "c" } }),
    );
    expect(event && isEvent(event) && event.seq).toBe(8);
  });

  it("parses signals, which have no seq", () => {
    const typing = parseServerMessage(
      JSON.stringify({
        type: "conversation.typing",
        id: "t",
        ts,
        data: { conversation_id: "c", actor: "operator", actor_id: "u", state: "start" },
      }),
    );
    expect(typing && isSignal(typing) && !isEvent(typing)).toBe(true);
    const viewers = parseServerMessage(
      JSON.stringify({ type: "conversation.viewers", id: "v", ts, data: { conversation_id: "c", user_ids: ["u"] } }),
    );
    expect(viewers && isSignal(viewers) && viewers.type === "conversation.viewers" && viewers.data.user_ids).toEqual([
      "u",
    ]);
  });

  it("accepts event types it does not know yet: new events are not breaking", () => {
    const event = parseServerMessage(JSON.stringify({ type: "sla.breached", id: "9", ts, seq: 10, data: {} }));
    expect(event && isEvent(event)).toBe(true);
  });

  it.each([
    ["not JSON", "hello"],
    ["not a string", 42],
    ["event without seq", JSON.stringify({ type: "contact.created", id: "1", ts, data: {} })],
    ["negative seq", JSON.stringify({ type: "resumed", id: "1", ts, data: { seq: -1 } })],
    ["hello without heartbeat", JSON.stringify({ type: "hello", id: "1", ts, data: { seq: 1 } })],
    ["unknown control", JSON.stringify({ type: "reboot", id: "1", ts, data: {} })],
    ["viewers without ids", JSON.stringify({ type: "conversation.viewers", id: "1", ts, data: { conversation_id: "c" } })],
  ])("rejects %s", (_, raw) => {
    expect(parseServerMessage(raw)).toBeUndefined();
  });
});

describe("realtimeUrl", () => {
  it("uses ws for http and wss for https", () => {
    expect(realtimeUrl("http://localhost:5173")).toBe("ws://localhost:5173/api/v1/realtime");
    expect(realtimeUrl("https://app.tolkovo.dev")).toBe("wss://app.tolkovo.dev/api/v1/realtime");
  });
});
