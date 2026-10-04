import { useLogout, useSession } from "@tolkovo/api-client/react";
import { Link, useNavigate } from "@tanstack/react-router";
import { Button } from "@tolkovo/ui";
import { CircleUserRound, LogOut } from "lucide-react";
import { useTranslation } from "react-i18next";

/** Who is signed in, a way to their profile (docs/07 §7) and the way out. */
export function UserMenu() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { data: session } = useSession();
  const logout = useLogout();

  if (!session) return null;

  return (
    <div className="flex items-center gap-1.5">
      {/* The visible name is the accessible name (WCAG 2.5.3); below 760 px only the icon shows. */}
      <Link
        to="/settings/profile"
        title={t("userMenu.profile")}
        className="flex h-8 max-w-[22ch] items-center gap-1.5 rounded-md px-1.5 text-[13px] text-ink-2 no-underline hover:bg-hover hover:text-ink"
      >
        <CircleUserRound aria-hidden="true" className="size-[18px] shrink-0" strokeWidth={1.7} />
        <span className="truncate max-nav:sr-only">{session.user.name}</span>
      </Link>
      <Button
        variant="ghost"
        size="icon"
        aria-label={t("auth.logout")}
        title={t("auth.logout")}
        disabled={logout.isPending}
        onClick={() => logout.mutate(undefined, { onSettled: () => void navigate({ to: "/login", replace: true }) })}
      >
        <LogOut aria-hidden="true" strokeWidth={1.7} />
      </Button>
    </div>
  );
}
