import { describe, expect, it } from "vitest";

import {
  LANGUAGE_KEY,
  TASK_PANEL_WIDTH_KEY,
  THEME_KEY,
  applyTheme,
  readStoredLanguage,
  readStoredTaskPanelWidth,
  readStoredTheme,
  storeTaskPanelWidth,
} from "./preferences";

describe("readStoredTaskPanelWidth", () => {
  it("returns the width the member dragged to", () => {
    window.localStorage.setItem(TASK_PANEL_WIDTH_KEY, "720");
    expect(readStoredTaskPanelWidth()).toBe(720);
  });

  // 379 is below the handle's minimum: the handle never stores it, so it is not a width of ours.
  it.each([null, "", "379", "99999", "12.5", "wide"])("falls back to 560 for %j", (value) => {
    if (value !== null) window.localStorage.setItem(TASK_PANEL_WIDTH_KEY, value);
    expect(readStoredTaskPanelWidth()).toBe(560);
  });

  it("forgets the default width", () => {
    storeTaskPanelWidth(720);
    storeTaskPanelWidth(560);
    expect(window.localStorage.getItem(TASK_PANEL_WIDTH_KEY)).toBeNull();
  });
});

describe("readStoredTheme", () => {
  it("returns the saved theme", () => {
    window.localStorage.setItem(THEME_KEY, "dark");
    expect(readStoredTheme()).toBe("dark");
  });

  it.each([null, "", "purple", "{\"theme\":\"dark\"}"])("falls back to system for %j", (value) => {
    if (value !== null) window.localStorage.setItem(THEME_KEY, value);
    expect(readStoredTheme()).toBe("system");
  });
});

describe("readStoredLanguage", () => {
  it("prefers the saved language", () => {
    window.localStorage.setItem(LANGUAGE_KEY, "en");
    expect(readStoredLanguage(["ru-RU"])).toBe("en");
  });

  it("ignores an invalid saved value and uses the first supported browser language", () => {
    window.localStorage.setItem(LANGUAGE_KEY, "de");
    expect(readStoredLanguage(["de-DE", "en-GB", "ru"])).toBe("en");
  });

  it("falls back to Russian when nothing matches", () => {
    expect(readStoredLanguage(["fr-FR"])).toBe("ru");
  });
});

describe("applyTheme", () => {
  it("sets data-theme for an explicit theme and removes it for system", () => {
    const root = document.createElement("html");
    applyTheme("light", root);
    expect(root.getAttribute("data-theme")).toBe("light");
    applyTheme("system", root);
    expect(root.hasAttribute("data-theme")).toBe(false);
  });
});
