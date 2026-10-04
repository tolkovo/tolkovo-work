import { Link, Outlet, createFileRoute } from "@tanstack/react-router";
import { cn } from "@tolkovo/ui";
import { useTranslation } from "react-i18next";

export const Route = createFileRoute("/_app/settings")({
  component: SettingsLayout,
});

// Workspace settings grouped by task (docs/07 §7). More sections join with their features.
const sections = [
  { to: "/settings/general", label: "settings.general.title" },
  { to: "/settings/team", label: "settings.team.title" },
] as const;

function SettingsLayout() {
  const { t } = useTranslation();
  return (
    <div className="flex flex-col gap-[18px] px-5 py-[18px] max-nav:px-4">
      <h1 className="m-0 font-display text-lg leading-tight font-semibold tracking-[-0.01em]">
        {t("pages.settings.title")}
      </h1>
      <nav aria-label={t("settings.nav")} className="flex gap-1 border-b border-line">
        {sections.map(({ to, label }) => (
          <Link
            key={to}
            to={to}
            className={cn(
              "-mb-px border-b-2 border-transparent px-3 py-2 text-[13px] text-ink-2 no-underline",
              "hover:text-ink aria-[current=page]:border-teal aria-[current=page]:font-medium aria-[current=page]:text-ink",
            )}
          >
            {t(label)}
          </Link>
        ))}
      </nav>
      <Outlet />
    </div>
  );
}
