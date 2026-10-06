import { createFileRoute } from "@tanstack/react-router";

import { RoutingSettingsPage } from "../../../components/settings/routing-settings";

export const Route = createFileRoute("/_app/settings/routing")({
  component: RoutingSettingsPage,
});
