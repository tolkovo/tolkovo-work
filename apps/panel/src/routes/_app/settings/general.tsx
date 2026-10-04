import { useSession } from "@tolkovo/api-client/react";
import { createFileRoute } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";

import { WorkspaceForm } from "../../../components/settings/workspace-form";

export const Route = createFileRoute("/_app/settings/general")({
  component: GeneralSettingsPage,
});

function GeneralSettingsPage() {
  const { t } = useTranslation();
  const { data: session } = useSession();
  // The shell renders only with a session (the /_app guard).
  if (!session) return null;
  return (
    <section aria-labelledby="settings-general-title" className="flex flex-col gap-3">
      <h2 id="settings-general-title" className="m-0 text-base font-semibold">
        {t("settings.general.title")}
      </h2>
      <p className="m-0 text-[13px] text-ink-2">
        {t("settings.general.address", { slug: session.workspace.slug })}
      </p>
      {/* key: a workspace switch remounts the form with the new values. */}
      <WorkspaceForm key={session.workspace.id} workspace={session.workspace} />
    </section>
  );
}
