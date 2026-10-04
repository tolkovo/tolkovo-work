import { Outlet } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";

import { LogoMark, Wordmark } from "../logo";
import { LanguageSwitcher } from "../shell/language-switcher";
import { ThemeSwitcher } from "../shell/theme-switcher";

/** Sign-in and sign-up screens: outside the panel shell, one card in the middle. */
export function AuthLayout() {
  const { t } = useTranslation();
  return (
    <div className="flex min-h-dvh flex-col bg-paper">
      <header className="flex items-center justify-end gap-2 p-3">
        <LanguageSwitcher />
        <ThemeSwitcher />
      </header>
      <main className="flex flex-1 flex-col items-center px-4 pt-[6vh] pb-12">
        <div className="flex w-full max-w-[400px] flex-col gap-5">
          <div className="flex flex-col items-center gap-2 text-center">
            <div className="flex items-center gap-2.5">
              <LogoMark />
              <Wordmark className="text-xl" />
            </div>
            <p className="m-0 max-w-[34ch] text-[13px] text-ink-2">{t("auth.tagline")}</p>
          </div>
          <Outlet />
        </div>
      </main>
    </div>
  );
}
