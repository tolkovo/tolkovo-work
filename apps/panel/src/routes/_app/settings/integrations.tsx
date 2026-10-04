import { createFileRoute } from "@tanstack/react-router";

import { IntegrationsSettings } from "../../../components/settings/integrations";
import { validateIntegrationsSearch } from "../../../lib/integrations/github";

export const Route = createFileRoute("/_app/settings/integrations")({
  validateSearch: validateIntegrationsSearch,
  component: IntegrationsPage,
});

function IntegrationsPage() {
  return <IntegrationsSettings search={Route.useSearch()} />;
}
