import { createFileRoute } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";

import { PasswordForm } from "../../../components/settings/password-form";
import { SessionsList } from "../../../components/settings/sessions-list";

export const Route = createFileRoute("/_app/settings/security")({
  component: SecurityPage,
});

function SecurityPage() {
  const { t } = useTranslation();
  return (
    <section aria-labelledby="settings-security-title" className="flex flex-col gap-6">
      <h2 id="settings-security-title" className="m-0 text-base font-semibold">
        {t("settings.security.title")}
      </h2>
      <PasswordForm />
      <SessionsList />
    </section>
  );
}
