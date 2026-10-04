import { isLanguage, languages, resources, type Language, type Messages } from "@tolkovo/i18n";
import i18next, { type i18n } from "i18next";
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
 * Creates the i18next instance: ICU message format for plurals (docs/00 §7.5),
 * both dictionaries bundled (they are small; split into lazy namespaces when they grow).
 */
export function createI18n(language: Language = readStoredLanguage()): i18n {
  const instance = i18next.createInstance();
  void instance
    .use(ICU)
    .use(initReactI18next)
    .init({
      resources,
      lng: language,
      fallbackLng: "ru",
      supportedLngs: languages,
      defaultNS: "translation",
      interpolation: { escapeValue: false }, // React escapes output
      returnNull: false,
      initAsync: false,
    });
  syncDocumentLanguage(language);
  instance.on("languageChanged", (lng) => {
    if (isLanguage(lng)) {
      syncDocumentLanguage(lng);
      storeLanguage(lng);
    }
  });
  return instance;
}

function syncDocumentLanguage(language: Language): void {
  document.documentElement.lang = language;
}
