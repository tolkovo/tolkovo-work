import en from "./en.json";
import type { Messages } from "./index";
import ru from "./ru.json";

/**
 * i18next resources with both dictionaries at once: one `translation` namespace per language. For
 * tests and the key check; the panel loads one language at a time (loadMessages).
 */
export const resources = {
  ru: { translation: ru },
  en: { translation: en satisfies Messages },
} as const;
