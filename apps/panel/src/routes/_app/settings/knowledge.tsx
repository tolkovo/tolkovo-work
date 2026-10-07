import { createFileRoute } from "@tanstack/react-router";

import { KnowledgeSettingsPage } from "../../../components/settings/knowledge-settings";

export const Route = createFileRoute("/_app/settings/knowledge")({
  component: KnowledgeSettingsPage,
});
