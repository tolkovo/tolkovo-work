import { useSession } from "@tolkovo/api-client/react";
import { Link, Outlet, createFileRoute } from "@tanstack/react-router";
import { cn } from "@tolkovo/ui";
import { useTranslation } from "react-i18next";

import { visibleSettingsGroups } from "../../components/settings/sections";

export const Route = createFileRoute("/_app/settings")({
  component: SettingsLayout,
});

/**
 * Settings grouped by task (docs/07 §7): «Пространство» for those with the rights, «Лично для
 * меня» for everyone. A side menu on wide screens, a stack above the section below 760 px.
 */
function SettingsLayout() {
  const { t } = useTranslation();
  const { data: session } = useSession();
  const groups = visibleSettingsGroups(session);
  return (
    <div className="flex flex-col gap-[18px] px-5 py-[18px] max-nav:px-4">
      <h1 className="m-0 font-display text-lg leading-tight font-semibold tracking-[-0.01em]">
        {t("pages.settings.title")}
      </h1>
      <div className="grid grid-cols-[200px_minmax(0,1fr)] items-start gap-6 max-nav:grid-cols-[minmax(0,1fr)] max-nav:gap-4">
        <nav aria-label={t("settings.nav")} className="flex flex-col gap-4 max-nav:flex-row max-nav:flex-wrap">
          {groups.map((group) => (
            <div key={group.label} className="flex min-w-[160px] flex-col gap-0.5">
              <h2 className="m-0 px-2.5 pb-1 text-[11.5px] font-medium tracking-wide text-ink-3 uppercase">
                {t(group.label)}
              </h2>
              <ul className="m-0 flex list-none flex-col gap-0.5 p-0">
                {group.sections.map(({ to, label }) => (
                  <li key={to}>
                    <Link
                      to={to}
                      className={cn(
                        "block rounded-md px-2.5 py-1.5 text-[13px] text-ink-2 no-underline",
                        "hover:bg-hover hover:text-ink",
                        "aria-[current=page]:bg-surface aria-[current=page]:font-medium aria-[current=page]:text-ink",
                        "aria-[current=page]:shadow-[inset_0_0_0_1px_var(--line)]",
                      )}
                    >
                      {t(label)}
                    </Link>
                  </li>
                ))}
              </ul>
            </div>
          ))}
        </nav>
        <div className="min-w-0">
          <Outlet />
        </div>
      </div>
    </div>
  );
}
