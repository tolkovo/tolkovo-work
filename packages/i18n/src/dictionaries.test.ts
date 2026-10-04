import { IntlMessageFormat } from "intl-messageformat";
import { describe, expect, it } from "vitest";

import { flattenKeys, languages, resources } from "./index";

function leaves(tree: Record<string, unknown>): [string, unknown][] {
  return flattenKeys(tree).map((key) => [
    key,
    key.split(".").reduce<unknown>((node, part) => (node as Record<string, unknown>)[part], tree),
  ]);
}

describe("dictionaries", () => {
  const ruKeys = flattenKeys(resources.ru.translation).sort();

  it.each(languages)("%s has exactly the same keys as ru", (lang) => {
    const keys = flattenKeys(resources[lang].translation).sort();
    const missing = ruKeys.filter((k) => !keys.includes(k));
    const extra = keys.filter((k) => !ruKeys.includes(k));
    expect({ missing, extra }).toEqual({ missing: [], extra: [] });
  });

  it.each(languages)("%s messages are non-empty strings in valid ICU syntax", (lang) => {
    for (const [key, value] of leaves(resources[lang].translation)) {
      expect(typeof value, key).toBe("string");
      expect((value as string).trim(), key).not.toBe("");
      expect(() => new IntlMessageFormat(value as string, lang), key).not.toThrow();
    }
  });

  it("flattens nested keys with dots", () => {
    expect(flattenKeys({ a: { b: "x", c: { d: "y" } }, e: "z" })).toEqual(["a.b", "a.c.d", "e"]);
  });
});
