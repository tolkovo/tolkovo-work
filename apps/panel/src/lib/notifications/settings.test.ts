import { beforeEach, describe, expect, it } from "vitest";

import { DEFAULT_NOTIFY_SETTINGS, NOTIFY_SETTINGS_KEY, readNotifySettings, useNotifySettings } from "./settings";
import { unreadTitle } from "./use-unread-title";

describe("notification settings of the browser", () => {
  beforeEach(() => {
    window.localStorage.clear();
    useNotifySettings.setState(DEFAULT_NOTIFY_SETTINGS);
  });

  it("falls back to the defaults for missing or broken storage", () => {
    expect(readNotifySettings()).toEqual(DEFAULT_NOTIFY_SETTINGS);
    window.localStorage.setItem(NOTIFY_SETTINGS_KEY, "{not json");
    expect(readNotifySettings()).toEqual(DEFAULT_NOTIFY_SETTINGS);
    window.localStorage.setItem(NOTIFY_SETTINGS_KEY, JSON.stringify({ volume: 7 }));
    expect(readNotifySettings()).toEqual(DEFAULT_NOTIFY_SETTINGS);
  });

  it("keeps changes for the next visit", () => {
    const s = useNotifySettings.getState();
    s.setSound("queued", false);
    s.setVolume(1.5);
    s.setDesktop(false);
    expect(readNotifySettings()).toEqual({
      sounds: { assigned: true, message: true, queued: false },
      volume: 1,
      desktop: false,
    });
  });
});

describe("unreadTitle", () => {
  it("counts like the list badges", () => {
    expect(unreadTitle(0)).toBe("Tolkovo");
    expect(unreadTitle(3)).toBe("(3) Tolkovo");
    expect(unreadTitle(120)).toBe("(99+) Tolkovo");
  });
});
