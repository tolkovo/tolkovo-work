import type { Language } from "@tolkovo/i18n";
import { resources } from "@tolkovo/i18n/resources";
import i18next, { type i18n } from "i18next";

import { setUpI18n } from "../lib/i18n";

/** An i18next instance with both dictionaries, ready at once: tests render synchronously. */
export function createI18n(language: Language = "ru"): i18n {
  const instance = i18next.createInstance();
  void setUpI18n(instance, language, { resources, fallbackLng: "ru", initAsync: false });
  return instance;
}
