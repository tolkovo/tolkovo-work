import { en } from "./en";
import { ru } from "./ru";
import type { Dict, Locale } from "./types";

export type { Dict, Locale } from "./types";

const dicts: Record<Locale, Dict> = { ru, en };

export function getDict(locale: Locale): Dict {
  return dicts[locale];
}

export const LOCALES: ReadonlyArray<Locale> = ["ru", "en"];
