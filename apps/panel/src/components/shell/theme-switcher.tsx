import { Monitor, Moon, Sun } from "lucide-react";
import { useTranslation } from "react-i18next";

import { usePreferences, type Theme } from "../../lib/preferences";
import { Segmented } from "./segmented";

const icons = { system: Monitor, light: Sun, dark: Moon } as const;

/** System → light → dark (docs/07 §2). */
export function ThemeSwitcher() {
  const { t } = useTranslation();
  const theme = usePreferences((s) => s.theme);
  const setTheme = usePreferences((s) => s.setTheme);

  const options = (["system", "light", "dark"] as const satisfies readonly Theme[]).map((value) => {
    const Icon = icons[value];
    return {
      value,
      label: t(`topbar.theme.${value}`),
      content: <Icon aria-hidden="true" strokeWidth={1.7} />,
      iconOnly: true,
    };
  });

  return <Segmented label={t("topbar.theme.label")} value={theme} options={options} onChange={setTheme} />;
}
