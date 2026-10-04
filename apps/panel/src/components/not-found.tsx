import { Link } from "@tanstack/react-router";
import { Button } from "@tolkovo/ui";
import { useTranslation } from "react-i18next";

import { LogoMark } from "./logo";

export function NotFound() {
  const { t } = useTranslation();
  return (
    <main className="grid min-h-dvh place-items-center bg-paper p-4">
      <section aria-labelledby="not-found-title" className="flex max-w-[440px] flex-col items-center gap-3 text-center">
        <LogoMark />
        <p className="m-0 font-mono text-[40px] leading-none font-medium tracking-[-0.02em] text-ink-3 tabular-nums">
          {t("notFound.code")}
        </p>
        <h1 id="not-found-title" className="m-0 font-display text-lg font-semibold">
          {t("notFound.title")}
        </h1>
        <p className="m-0 text-ink-2">{t("notFound.text")}</p>
        <Button asChild variant="primary" size="lg" className="mt-1">
          <Link to="/inbox">{t("notFound.action")}</Link>
        </Button>
      </section>
    </main>
  );
}
