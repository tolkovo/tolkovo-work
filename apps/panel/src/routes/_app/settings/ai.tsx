import { createFileRoute } from "@tanstack/react-router";

import { AISettingsPage } from "../../../components/settings/ai-settings";

export const Route = createFileRoute("/_app/settings/ai")({
  component: AISettingsPage,
});
