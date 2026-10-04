import { useSession } from "@tolkovo/api-client/react";
import { createFileRoute } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";

import { ProfileForm } from "../../../components/settings/profile-form";

export const Route = createFileRoute("/_app/settings/profile")({
  component: ProfilePage,
});

function ProfilePage() {
  const { t } = useTranslation();
  const { data: session } = useSession();
  // The shell renders only with a session (the /_app guard).
  if (!session) return null;
  return (
    <section aria-labelledby="settings-profile-title" className="flex flex-col gap-3">
      <h2 id="settings-profile-title" className="m-0 text-base font-semibold">
        {t("settings.profile.title")}
      </h2>
      {/* key: a new session (another user after sign-in) remounts the form with its values. */}
      <ProfileForm key={session.user.id} user={session.user} />
    </section>
  );
}
