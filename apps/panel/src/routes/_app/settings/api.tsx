import { createFileRoute } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";

import { ApiAccess } from "../../../components/settings/api-access";

export const Route = createFileRoute("/_app/settings/api")({
  component: ApiPage,
});

function ApiPage() {
  const { t } = useTranslation();
  return (
    <section aria-labelledby="settings-api-title" className="flex flex-col gap-6">
      <h2 id="settings-api-title" className="m-0 text-base font-semibold">
        {t("settings.api.title")}
      </h2>
      <ApiAccess />
    </section>
  );
}
