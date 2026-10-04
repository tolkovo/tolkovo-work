import { useSession } from "@tolkovo/api-client/react";
import { Outlet, useRouter } from "@tanstack/react-router";
import { useEffect } from "react";
import { useTranslation } from "react-i18next";

import { Rail } from "./rail";
import { Topbar } from "./topbar";

/** Panel layout: section rail + top bar + the current section (docs/07 §3). */
export function AppShell() {
  const { t } = useTranslation();
  const { data: session } = useSession();
  const router = useRouter();

  // The session ended while the panel was open (expired, revoked, signed out in another tab).
  // Re-running the /_app guard redirects to sign-in with the current location as return path;
  // a <Navigate> here would re-render against the pending location and nest the parameter.
  useEffect(() => {
    if (session === null) void router.invalidate();
  }, [session, router]);
  if (session === null) return null;

  return (
    <div className="grid h-dvh grid-cols-[68px_minmax(0,1fr)] max-nav:grid-cols-[minmax(0,1fr)] max-nav:grid-rows-[minmax(0,1fr)_58px]">
      <a
        href="#main"
        className="sr-only z-50 rounded-md bg-ink px-3 py-2 text-paper focus:not-sr-only focus:fixed focus:top-2 focus:left-2"
      >
        {t("app.skipToContent")}
      </a>
      <Rail />
      <div className="grid min-h-0 min-w-0 grid-rows-[48px_minmax(0,1fr)] max-nav:row-start-1">
        <Topbar />
        <main id="main" tabIndex={-1} className="min-h-0 min-w-0 overflow-auto outline-none">
          <Outlet />
        </main>
      </div>
    </div>
  );
}
