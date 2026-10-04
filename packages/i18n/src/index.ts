import en from "./en.json";
import ru from "./ru.json";

/** Interface languages, in the order they are offered to people. */
export const languages = ["ru", "en"] as const;
export type Language = (typeof languages)[number];

export const defaultLanguage: Language = "ru";

/** Language names in the language itself (endonyms): shown as is, never translated. */
export const languageNames: Record<Language, string> = {
  ru: "Русский",
  en: "English",
};

/** Message tree. Russian is the reference dictionary; English must have the same keys. */
export type Messages = typeof ru;

/** i18next resources: one `translation` namespace per language. */
export const resources = {
  ru: { translation: ru },
  en: { translation: en satisfies Messages },
} as const;

export function isLanguage(value: unknown): value is Language {
  return typeof value === "string" && (languages as readonly string[]).includes(value);
}

/** Flattens a message tree into dot-separated keys: { a: { b: "x" } } → ["a.b"]. */
export function flattenKeys(tree: Record<string, unknown>, prefix = ""): string[] {
  return Object.entries(tree).flatMap(([key, value]) => {
    const path = prefix ? `${prefix}.${key}` : key;
    return typeof value === "object" && value !== null
      ? flattenKeys(value as Record<string, unknown>, path)
      : [path];
  });
}
