import { isLanguage, languages, loadMessages, type Language, type Messages } from "@tolkovo/i18n";
import i18next, { type BackendModule, type i18n, type InitOptions } from "i18next";
import ICU from "i18next-icu";
import { initReactI18next } from "react-i18next";

import { readStoredLanguage, storeLanguage } from "./preferences";

declare module "i18next" {
  interface CustomTypeOptions {
    defaultNS: "translation";
    resources: { translation: Messages };
  }
}

/**
 * Loads a dictionary when i18next needs a language: at start and on a switch. Each dictionary is
 * a chunk of its own (about 25 KB gzip), so the first screen carries only the language in use.
 */
const lazyDictionaries: BackendModule = {
  type: "backend",
  init() {},
  read(language, _namespace, callback) {
    if (!isLanguage(language)) {
      callback(null, {});
      return;
    }
    loadMessages(language).then(
      (messages) => callback(null, messages),
      (err: unknown) => callback(err instanceof Error ? err : new Error(String(err)), null),
    );
  },
};

/**
 * The panel's i18next instance: ICU message format for plurals (docs/00 §7.5), the dictionary of
 * the language in use loaded before the first render, another one when the person switches.
 */
export async function loadI18n(language: Language = readStoredLanguage()): Promise<i18n> {
  const instance = i18next.createInstance().use(lazyDictionaries);
  // Both dictionaries have the same keys (packages/i18n checks it): nothing to fall back to.
  await setUpI18n(instance, language, { fallbackLng: false, load: "currentOnly" });
  return instance;
}

/** Common setup of an instance; tests pass the dictionaries themselves (src/test/i18n.ts). */
export function setUpI18n(instance: i18n, language: Language, options: InitOptions): Promise<unknown> {
  const ready = instance
    .use(ICU)
    .use(initReactI18next)
    .init({
      lng: language,
      supportedLngs: languages,
      defaultNS: "translation",
      interpolation: { escapeValue: false }, // React escapes output
      returnNull: false,
      ...options,
    });
  syncDocumentLanguage(language);
  instance.on("languageChanged", (lng) => {
    if (isLanguage(lng)) {
      syncDocumentLanguage(lng);
      storeLanguage(lng);
    }
  });
  return ready;
}

function syncDocumentLanguage(language: Language): void {
  document.documentElement.lang = language;
}
