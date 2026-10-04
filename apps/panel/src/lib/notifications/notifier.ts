import type { Conversation } from "@tolkovo/api-client";
import type { TFunction } from "i18next";

import type { RealtimeEvent } from "../realtime/protocol";
import type { Leader } from "./leader";
import { CRITICAL_KINDS, desktopPermission, useNotifySettings, type AlertKind } from "./settings";
import type { SoundPlayer } from "./sound";

/**
 * Decides which realtime events deserve a sound or a desktop notification (docs/04 §5.5,
 * docs/07 §8). Critical: a conversation given to me by someone else, a customer message in my
 * conversation; informational: a new conversation in the queue (sound only). Nothing for the
 * conversation open in a visible tab: the person already sees it.
 */

/** Events older than this are a replay after a reconnect, not news. */
export const STALE_EVENT_MS = 2 * 60_000;

const str = (v: unknown): string | undefined => (typeof v === "string" ? v : undefined);

/** The most important alert the events give about the conversation, as it is now. */
export function classify(
  events: readonly RealtimeEvent[],
  c: Conversation,
  meId: string | undefined,
  now: number = Date.now(),
): AlertKind | null {
  if (!meId) return null;
  const fresh = events.filter((e) => now - Date.parse(e.ts) < STALE_EVENT_MS);
  const mine = c.assignee_id === meId;
  if (
    mine &&
    fresh.some(
      (e) =>
        e.type === "conversation.assigned" && str(e.data.assignee_id) === meId && str(e.data.actor_id) !== meId,
    )
  ) {
    return "assigned";
  }
  if (
    mine &&
    fresh.some((e) => e.type === "message.created" && e.data.sender_type === "contact" && e.data.is_private !== true)
  ) {
    return "message";
  }
  const waiting = c.status === "queued" && c.assignee_id === null;
  if (
    waiting &&
    fresh.some(
      (e) =>
        (e.type === "conversation.created" && e.data.status === "queued") ||
        (e.type === "conversation.status_changed" && e.data.to === "queued" && str(e.data.actor_id) !== meId),
    )
  ) {
    return "queued";
  }
  return null;
}

export type NotifierOptions = {
  sound: SoundPlayer;
  leader: Leader;
  t: TFunction;
  /** The conversation open in this tab, if any. */
  openConversationId: () => string | undefined;
  /** Opens a conversation when its desktop notification is clicked. */
  openConversation: (id: string) => void;
};

export type Notifier = {
  /** The conversation after a batch of its events, freshly fetched. */
  notify: (events: readonly RealtimeEvent[], c: Conversation, meId: string | undefined) => void;
  /** «Отправить тестовое уведомление»: a sound and a desktop notification, here and now. */
  test: () => void;
};

export function createNotifier({ sound, leader, t, openConversationId, openConversation }: NotifierOptions): Notifier {
  const showDesktop = (title: string, body: string, tag: string, onClick?: () => void) => {
    if (desktopPermission() !== "granted") return;
    try {
      const n = new Notification(title, { body, tag, icon: "/favicon.svg" });
      n.onclick = () => {
        window.focus();
        onClick?.();
        n.close();
      };
    } catch {
      // Some mobile browsers allow notifications only from a service worker (Web Push, later).
    }
  };

  return {
    notify(events, c, meId) {
      const kind = classify(events, c, meId);
      if (!kind || !leader.isLeader()) return;
      if (document.visibilityState === "visible" && openConversationId() === c.id) return;
      const settings = useNotifySettings.getState();
      if (settings.sounds[kind]) sound.play(kind, settings.volume);
      if (settings.desktop && CRITICAL_KINDS.includes(kind)) {
        showDesktop(t(`notifications.alert.${kind}`, { number: c.number }), c.contact.name, `conversation-${c.id}`, () =>
          openConversation(c.id),
        );
      }
    },
    test() {
      const settings = useNotifySettings.getState();
      sound.play("assigned", settings.volume);
      showDesktop(t("notifications.test.title"), t("notifications.test.body"), "tolkovo-test");
    },
  };
}
