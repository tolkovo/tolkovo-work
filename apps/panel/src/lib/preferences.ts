import { languages, type Language } from "@tolkovo/i18n";
import { z } from "zod";
import { create } from "zustand";

/**
 * Personal UI preferences: theme and interface language (UI state only, docs/00 §7.2).
 * Kept in localStorage until the profile API exists; then the server copy wins (docs/04 §8.2).
 * Storage keys and values must match public/theme-init.js.
 */

export const themes = ["system", "light", "dark"] as const;
export type Theme = (typeof themes)[number];

export const THEME_KEY = "tolkovo.theme";
export const LANGUAGE_KEY = "tolkovo.lang";

const themeSchema = z.enum(themes);
const languageSchema = z.enum(languages);

function readStorage(key: string): string | null {
  try {
    return window.localStorage.getItem(key);
  } catch {
    return null;
  }
}

function writeStorage(key: string, value: string | null): void {
  try {
    if (value === null) window.localStorage.removeItem(key);
    else window.localStorage.setItem(key, value);
  } catch {
    // Storage unavailable (privacy mode, quota): the choice lasts for this session only.
  }
}

/** Saved theme, or "system" when nothing valid is stored (localStorage is external input). */
export function readStoredTheme(): Theme {
  return themeSchema.catch("system").parse(readStorage(THEME_KEY));
}

/** Saved language, otherwise the browser language if supported, otherwise Russian. */
export function readStoredLanguage(navigatorLanguages: readonly string[] = navigator.languages): Language {
  const stored = languageSchema.safeParse(readStorage(LANGUAGE_KEY));
  if (stored.success) return stored.data;
  for (const tag of navigatorLanguages) {
    const base = languageSchema.safeParse(tag.toLowerCase().split("-")[0]);
    if (base.success) return base.data;
  }
  return "ru";
}

/** Applies the theme to <html>: "system" leaves it to prefers-color-scheme (docs/07 §2). */
export function applyTheme(theme: Theme, root: HTMLElement = document.documentElement): void {
  if (theme === "system") root.removeAttribute("data-theme");
  else root.setAttribute("data-theme", theme);
}

type PreferencesState = {
  theme: Theme;
  setTheme: (theme: Theme) => void;
};

export const usePreferences = create<PreferencesState>()((set) => ({
  theme: readStoredTheme(),
  setTheme: (theme) => {
    applyTheme(theme);
    writeStorage(THEME_KEY, theme === "system" ? null : theme);
    set({ theme });
  },
}));

export function storeLanguage(language: Language): void {
  writeStorage(LANGUAGE_KEY, language);
}

export const TASKS_LAYOUT_KEY = "tolkovo.tasks.layout";

/** The last view of the tasks section the member picked; a link with ?layout= wins over it. */
export function readStoredTasksLayout(): "list" | "board" | "flow" {
  return z.enum(["list", "board", "flow"]).catch("list").parse(readStorage(TASKS_LAYOUT_KEY));
}

export function storeTasksLayout(layout: "list" | "board" | "flow"): void {
  writeStorage(TASKS_LAYOUT_KEY, layout === "list" ? null : layout);
}

export const TASK_PANEL_WIDTH_KEY = "tolkovo.tasks.panelWidth";
export const TASK_PANEL_DEFAULT_WIDTH = 560;
/** The narrowest the open task beside the board gets; the handle and the stored width share it. */
export const TASK_PANEL_MIN_WIDTH = 380;

/** The width of the open task beside the board the member dragged it to, in px. */
export function readStoredTaskPanelWidth(): number {
  return z.coerce
    .number()
    .int()
    .min(TASK_PANEL_MIN_WIDTH)
    .max(4000)
    .catch(TASK_PANEL_DEFAULT_WIDTH)
    .parse(readStorage(TASK_PANEL_WIDTH_KEY) ?? undefined);
}

export function storeTaskPanelWidth(width: number): void {
  writeStorage(TASK_PANEL_WIDTH_KEY, width === TASK_PANEL_DEFAULT_WIDTH ? null : String(Math.round(width)));
}
