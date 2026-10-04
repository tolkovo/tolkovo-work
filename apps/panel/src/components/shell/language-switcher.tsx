import { useSession, useUpdateMe } from "@tolkovo/api-client/react";
import { isLanguage, languageNames, languages, type Language } from "@tolkovo/i18n";
import { useTranslation } from "react-i18next";

import { Segmented } from "./segmented";

/**
 * Interface language in the top bar. It switches at once and is saved to the profile, whose
 * copy wins on the next load (docs/04 §8.2); if saving fails, the profile language comes back.
 */
export function LanguageSwitcher() {
  const { t, i18n } = useTranslation();
  const { data: session } = useSession();
  const updateMe = useUpdateMe();
  const current: Language = isLanguage(i18n.resolvedLanguage) ? i18n.resolvedLanguage : "ru";

  const options = languages.map((value) => ({
    value,
    label: languageNames[value],
    content: value.toUpperCase(),
    lang: value,
  }));

  const onChange = (value: Language) => {
    void i18n.changeLanguage(value);
    if (!session || session.user.locale === value) return;
    const saved = session.user.locale;
    updateMe.mutate({ locale: value }, { onError: () => void i18n.changeLanguage(saved) });
  };

  return <Segmented label={t("topbar.language.label")} value={current} options={options} onChange={onChange} />;
}
