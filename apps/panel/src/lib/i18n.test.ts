import { describe, expect, it } from "vitest";

import { createI18n } from "./i18n";

describe("createI18n", () => {
  it("formats ICU plurals with the Russian forms one / few / many", () => {
    const i18n = createI18n("ru");
    i18n.addResource(
      "ru",
      "translation",
      "probe",
      "{count, plural, one {# диалог} few {# диалога} many {# диалогов} other {# диалога}}",
    );
    // "probe" is not a dictionary key, so step outside the typed t() for this check.
    const untypedT = i18n.t.bind(i18n) as unknown as (key: string, options: { count: number }) => string;
    const t = (count: number) => untypedT("probe", { count });
    expect([t(1), t(2), t(5), t(21), t(1.5)]).toEqual([
      "1 диалог",
      "2 диалога",
      "5 диалогов",
      "21 диалог",
      "1,5 диалога",
    ]);
  });

  it("sets the document language", () => {
    createI18n("en");
    expect(document.documentElement.lang).toBe("en");
  });
});
