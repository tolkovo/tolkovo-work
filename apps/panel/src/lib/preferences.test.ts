import { describe, expect, it } from "vitest";

import { LANGUAGE_KEY, THEME_KEY, applyTheme, readStoredLanguage, readStoredTheme } from "./preferences";

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
