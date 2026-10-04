import { isLanguage, languageNames, languages, type Language } from "@tolkovo/i18n";
import { useTranslation } from "react-i18next";

import { Segmented } from "./segmented";

export function LanguageSwitcher() {
  const { t, i18n } = useTranslation();
  const current: Language = isLanguage(i18n.resolvedLanguage) ? i18n.resolvedLanguage : "ru";

  const options = languages.map((value) => ({
    value,
    label: languageNames[value],
    content: value.toUpperCase(),
    lang: value,
  }));

  return (
    <Segmented
      label={t("topbar.language.label")}
      value={current}
      options={options}
      onChange={(value) => void i18n.changeLanguage(value)}
    />
  );
}
