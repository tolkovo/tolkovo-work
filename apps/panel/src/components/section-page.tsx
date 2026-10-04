import { Link, type LinkProps } from "@tanstack/react-router";
import { Button } from "@tolkovo/ui";
import type { LucideIcon } from "lucide-react";
import { useTranslation } from "react-i18next";

import { EmptyState } from "./empty-state";

type Section = "inbox" | "monitor" | "analytics" | "kb" | "settings";

type SectionPageProps = {
  section: Section;
  icon: LucideIcon;
  /** Where the empty-state call to action leads. */
  actionTo: LinkProps["to"];
};

/**
 * A panel section before its feature lands: page title and the empty state.
 * Loading (skeleton) and error states come from the router (pending and error components).
 */
export function SectionPage({ section, icon, actionTo }: SectionPageProps) {
  const { t } = useTranslation();
  return (
    <div className="flex flex-col gap-[18px] px-5 py-[18px] max-nav:px-4">
      <h1 className="m-0 font-display text-lg leading-tight font-semibold tracking-[-0.01em]">
        {t(`pages.${section}.title`)}
      </h1>
      <EmptyState
        icon={icon}
        title={t(`pages.${section}.empty.title`)}
        text={t(`pages.${section}.empty.text`)}
        action={
          <Button asChild variant="primary" size="lg">
            <Link to={actionTo}>{t(`pages.${section}.empty.action`)}</Link>
          </Button>
        }
      />
    </div>
  );
}
