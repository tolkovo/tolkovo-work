import { createFileRoute } from "@tanstack/react-router";

import { TopicSettingsPage } from "../../../components/settings/topic-settings";

export const Route = createFileRoute("/_app/settings/topics")({
  component: TopicSettingsPage,
});
