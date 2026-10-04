import { useLogout, useSession } from "@tolkovo/api-client/react";
import { useNavigate } from "@tanstack/react-router";
import { Button } from "@tolkovo/ui";
import { LogOut } from "lucide-react";
import { useTranslation } from "react-i18next";

/** Who is signed in, and the way out. Profile settings join it later (docs/07 §7). */
export function UserMenu() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { data: session } = useSession();
  const logout = useLogout();

  if (!session) return null;

  return (
    <div className="flex items-center gap-1.5">
      <span className="max-w-[18ch] truncate text-[13px] text-ink-2 max-nav:hidden" title={session.user.email}>
        {session.user.name}
      </span>
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
