import { createFileRoute } from "@tanstack/react-router";
import { cn } from "@tolkovo/ui";
import { Monitor, Moon, Sun } from "lucide-react";
import { useTranslation } from "react-i18next";

import { themes, usePreferences } from "../../../lib/preferences";

export const Route = createFileRoute("/_app/settings/appearance")({
  component: AppearancePage,
});

const icons = { system: Monitor, light: Sun, dark: Moon } as const;

/**
 * Theme for this browser (docs/07 §2). It stays in localStorage, not in the profile: a dark
 * night-shift monitor and a light laptop are both normal. Density and font size come when the
 * panel can apply them; they are not offered before that.
 */
function AppearancePage() {
  const { t } = useTranslation();
  const theme = usePreferences((s) => s.theme);
  const setTheme = usePreferences((s) => s.setTheme);
  return (
    <section aria-labelledby="settings-appearance-title" className="flex flex-col gap-3">
      <h2 id="settings-appearance-title" className="m-0 text-base font-semibold">
        {t("settings.appearance.title")}
      </h2>
      <fieldset aria-describedby="appearance-theme-hint" className="m-0 flex max-w-[480px] flex-col gap-2 border-0 p-0">
        <legend className="mb-1.5 p-0 text-[13px] font-medium text-ink">{t("settings.appearance.theme")}</legend>
        <div className="flex flex-wrap gap-2">
          {themes.map((value) => {
            const Icon = icons[value];
            return (
              <label
                key={value}
                className={cn(
                  "flex cursor-pointer items-center gap-2 rounded-lg border border-line bg-surface px-3 py-2 text-[13px] text-ink-2",
                  "hover:bg-hover hover:text-ink has-checked:border-teal has-checked:text-ink",
                  "has-focus-visible:outline-2 has-focus-visible:outline-offset-2 has-focus-visible:outline-ring",
                )}
              >
                <input
                  type="radio"
                  name="theme"
                  value={value}
                  checked={theme === value}
                  onChange={() => setTheme(value)}
                  className="sr-only"
                />
                <Icon aria-hidden="true" className="size-4" strokeWidth={1.7} />
                {t(`topbar.theme.${value}`)}
              </label>
            );
          })}
        </div>
        <p id="appearance-theme-hint" className="m-0 text-[12.5px] text-ink-3">
          {t("settings.appearance.themeHint")}
        </p>
      </fieldset>
    </section>
  );
}
