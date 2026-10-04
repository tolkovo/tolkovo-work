import { hasPermission } from "@tolkovo/api-client";
import { useSession } from "@tolkovo/api-client/react";
import { Link } from "@tanstack/react-router";
import { cn } from "@tolkovo/ui";
import { useTranslation } from "react-i18next";

import { LogoMark } from "../logo";
import { navItems } from "./nav-items";

/** Section rail: on the left on wide screens, at the bottom below 760 px (docs/07 §3). */
export function Rail() {
  const { t } = useTranslation();
  const { data: session } = useSession();
  const items = navItems.filter((item) => !item.permission || hasPermission(session, item.permission));
  return (
    <nav
      aria-label={t("nav.label")}
      className={cn(
        "flex flex-col items-center gap-1 border-r border-line bg-sunk pt-2.5 pb-3",
        "max-nav:row-start-2 max-nav:flex-row max-nav:justify-around max-nav:border-t max-nav:border-r-0",
        "max-nav:px-2 max-nav:pt-1 max-nav:pb-[calc(4px+env(safe-area-inset-bottom,0px))]",
      )}
    >
      <LogoMark className="mb-3 max-nav:hidden" />
      {items.map(({ to, label, icon: Icon }) => (
        <Link
          key={to}
          to={to}
          className={cn(
            "relative flex w-14 flex-col items-center gap-[3px] rounded-[10px] pt-[7px] pb-1.5",
            "text-[10.5px] text-ink-2 no-underline hover:bg-hover hover:text-ink",
            "aria-[current=page]:bg-surface aria-[current=page]:text-ink",
            "aria-[current=page]:shadow-[inset_0_0_0_1px_var(--line)]",
          )}
        >
          <Icon aria-hidden="true" className="size-[18px]" strokeWidth={1.7} />
          {t(label)}
        </Link>
      ))}
    </nav>
  );
}
