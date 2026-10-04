import { z } from "zod";
import { create } from "zustand";

/**
 * Sounds and desktop notifications of this browser (docs/04 §5.5). They are settings of a
 * device, not of the account: a muted laptop in a meeting and a loud desk monitor are both
 * normal, so they stay in localStorage. The email about missed messages is on the server.
 */

/** What the panel alerts about. Critical kinds also show a desktop notification (docs/07 §8). */
export const ALERT_KINDS = ["assigned", "message", "queued"] as const;
export type AlertKind = (typeof ALERT_KINDS)[number];
export const CRITICAL_KINDS: readonly AlertKind[] = ["assigned", "message"];

export const NOTIFY_SETTINGS_KEY = "tolkovo.notify";

const settingsSchema = z.object({
  sounds: z.object({ assigned: z.boolean(), message: z.boolean(), queued: z.boolean() }),
  volume: z.number().min(0).max(1),
  desktop: z.boolean(),
});
export type NotifySettings = z.infer<typeof settingsSchema>;

export const DEFAULT_NOTIFY_SETTINGS: NotifySettings = {
  sounds: { assigned: true, message: true, queued: true },
  volume: 0.6,
  // Takes effect only once the browser permission is granted, which the person asks for.
  desktop: true,
};

/** Stored settings, or the defaults when nothing valid is stored (localStorage is external input). */
export function readNotifySettings(): NotifySettings {
  try {
    const raw = window.localStorage.getItem(NOTIFY_SETTINGS_KEY);
    if (raw === null) return DEFAULT_NOTIFY_SETTINGS;
    return settingsSchema.catch(DEFAULT_NOTIFY_SETTINGS).parse(JSON.parse(raw));
  } catch {
    return DEFAULT_NOTIFY_SETTINGS;
  }
}

function writeNotifySettings(settings: NotifySettings): void {
  try {
    window.localStorage.setItem(NOTIFY_SETTINGS_KEY, JSON.stringify(settings));
  } catch {
    // Storage unavailable (privacy mode, quota): the choice lasts for this session only.
  }
}

type NotifySettingsState = NotifySettings & {
  setSound: (kind: AlertKind, on: boolean) => void;
  setVolume: (volume: number) => void;
  setDesktop: (on: boolean) => void;
};

const pick = ({ sounds, volume, desktop }: NotifySettings): NotifySettings => ({ sounds, volume, desktop });

export const useNotifySettings = create<NotifySettingsState>((set, get) => {
  const update = (patch: Partial<NotifySettings>) => {
    const next = { ...pick(get()), ...patch };
    writeNotifySettings(next);
    set(next);
  };
  return {
    ...readNotifySettings(),
    setSound: (kind, on) => update({ sounds: { ...get().sounds, [kind]: on } }),
    setVolume: (volume) => update({ volume: Math.min(1, Math.max(0, volume)) }),
    setDesktop: (desktop) => update({ desktop }),
  };
});

/** The browser's answer about desktop notifications; "unsupported" without the Notification API. */
export type DesktopPermission = NotificationPermission | "unsupported";

export function desktopPermission(): DesktopPermission {
  return typeof Notification === "undefined" ? "unsupported" : Notification.permission;
}
