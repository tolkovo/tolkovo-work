import type { Conversation } from "@tolkovo/api-client";
import type { TFunction } from "i18next";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { RealtimeEvent } from "../realtime/protocol";
import { classify, createNotifier, STALE_EVENT_MS } from "./notifier";
import { DEFAULT_NOTIFY_SETTINGS, useNotifySettings } from "./settings";

const ME = "u-me";
const NOW = Date.parse("2026-10-04T12:00:30Z");

const conv = (patch: Partial<Conversation> = {}): Conversation => ({
  id: "c1",
  number: 7,
  status: "assigned",
  priority: 0,
  line_level: 1,
  contact: { id: "k1", name: "Марина Ковалёва" },
  assignee_id: ME,
  channel_id: null,
  subject: null,
  first_response_at: null,
  last_message_at: "2026-10-04T12:00:00Z",
  resolved_at: null,
  closed_at: null,
  awaiting_fix: false,
  version: 2,
  created_at: "2026-10-04T12:00:00Z",
  updated_at: "2026-10-04T12:00:00Z",
  last_message: null,
  tag_ids: [],
  last_read_message_id: null,
  unread_count: 1,
  ...patch,
});

const ev = (type: string, data: Record<string, unknown>, ts = "2026-10-04T12:00:00Z"): RealtimeEvent => ({
  type,
  id: "e1",
  ts,
  seq: 1,
  data: { conversation_id: "c1", ...data },
});

const assignedToMe = (actor: string | null) =>
  ev("conversation.assigned", { assignee_id: ME, previous_assignee_id: null, actor_id: actor });
const customerMessage = ev("message.created", { sender_type: "contact", is_private: false, kind: "text" });

describe("classify", () => {
  it("alerts about a conversation given to me by someone else, not one I took", () => {
    expect(classify([assignedToMe("u-boss")], conv(), ME, NOW)).toBe("assigned");
    expect(classify([assignedToMe(ME)], conv(), ME, NOW)).toBeNull();
    // Given and taken away again within the batch: the fresh state wins.
    expect(classify([assignedToMe("u-boss")], conv({ assignee_id: "u-other" }), ME, NOW)).toBeNull();
  });

  it("alerts about customer messages in my conversations only", () => {
    expect(classify([customerMessage], conv(), ME, NOW)).toBe("message");
    expect(classify([customerMessage], conv({ assignee_id: "u-other" }), ME, NOW)).toBeNull();
    const note = ev("message.created", { sender_type: "operator", is_private: true, kind: "note" });
    expect(classify([note], conv(), ME, NOW)).toBeNull();
  });

  it("alerts about new conversations in the queue and returns by others", () => {
    const queued = conv({ status: "queued", assignee_id: null });
    expect(classify([ev("conversation.created", { status: "queued" })], queued, ME, NOW)).toBe("queued");
    const back = (actor: string) => ev("conversation.status_changed", { from: "assigned", to: "queued", actor_id: actor });
    expect(classify([back("u-boss")], queued, ME, NOW)).toBe("queued");
    expect(classify([back(ME)], queued, ME, NOW)).toBeNull();
    // Someone took it before the fetch came back.
    expect(classify([ev("conversation.created", { status: "queued" })], conv(), ME, NOW)).toBeNull();
  });

  it("prefers the critical alert and ignores replays", () => {
    expect(classify([customerMessage, assignedToMe("u-boss")], conv(), ME, NOW)).toBe("assigned");
    const old = new Date(NOW - STALE_EVENT_MS - 1000).toISOString();
    expect(classify([ev("message.created", { sender_type: "contact" }, old)], conv(), ME, NOW)).toBeNull();
    expect(classify([customerMessage], conv(), undefined, NOW)).toBeNull();
  });
});

describe("createNotifier", () => {
  const shown: { title: string; options?: NotificationOptions; onclick?: (() => void) | null }[] = [];

  class FakeNotification {
    static permission: NotificationPermission = "granted";
    onclick: (() => void) | null = null;
    constructor(title: string, options?: NotificationOptions) {
      shown.push(this as unknown as (typeof shown)[number]);
      Object.assign(this, { title, options });
    }
    close() {}
  }

  beforeEach(() => {
    vi.useFakeTimers({ now: NOW, toFake: ["Date"] });
    vi.stubGlobal("Notification", FakeNotification);
    vi.spyOn(window, "focus").mockImplementation(() => undefined); // jsdom has no window focus
    shown.length = 0;
    useNotifySettings.setState(DEFAULT_NOTIFY_SETTINGS);
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  function setup({ leader = true, open }: { leader?: boolean; open?: string } = {}) {
    const sound = { play: vi.fn(), unlock: vi.fn() };
    const openConversation = vi.fn();
    const t = ((key: string, o?: { number?: number }) => (o?.number ? `${key} ${o.number}` : key)) as unknown as TFunction;
    const notifier = createNotifier({
      sound,
      leader: { isLeader: () => leader, stop: () => undefined },
      t,
      openConversationId: () => open,
      openConversation,
    });
    return { notifier, sound, openConversation };
  }

  it("plays the sound and shows a desktop notification that opens the conversation", () => {
    const { notifier, sound, openConversation } = setup();
    notifier.notify([assignedToMe("u-boss")], conv(), ME);
    expect(sound.play).toHaveBeenCalledWith("assigned", 0.6);
    expect(shown).toHaveLength(1);
    expect(shown[0]).toMatchObject({
      title: "notifications.alert.assigned 7",
      options: { body: "Марина Ковалёва", tag: "conversation-c1" },
    });
    shown[0]?.onclick?.();
    expect(openConversation).toHaveBeenCalledWith("c1");
  });

  it("only rings for the queue: it is not critical", () => {
    const { notifier, sound } = setup();
    notifier.notify([ev("conversation.created", { status: "queued" })], conv({ status: "queued", assignee_id: null }), ME);
    expect(sound.play).toHaveBeenCalledWith("queued", 0.6);
    expect(shown).toHaveLength(0);
  });

  it("stays quiet in other tabs, for the open conversation and for muted kinds", () => {
    const follower = setup({ leader: false });
    follower.notifier.notify([customerMessage], conv(), ME);
    expect(follower.sound.play).not.toHaveBeenCalled();

    const watching = setup({ open: "c1" });
    watching.notifier.notify([customerMessage], conv(), ME);
    expect(watching.sound.play).not.toHaveBeenCalled();
    expect(shown).toHaveLength(0);

    useNotifySettings.getState().setSound("message", false);
    useNotifySettings.getState().setDesktop(false);
    const muted = setup();
    muted.notifier.notify([customerMessage], conv(), ME);
    expect(muted.sound.play).not.toHaveBeenCalled();
    expect(shown).toHaveLength(0);
  });

  it("shows nothing on the desktop without the browser's permission", () => {
    FakeNotification.permission = "denied";
    const { notifier, sound } = setup();
    notifier.notify([customerMessage], conv(), ME);
    expect(sound.play).toHaveBeenCalled();
    expect(shown).toHaveLength(0);
    FakeNotification.permission = "granted";
  });
});
