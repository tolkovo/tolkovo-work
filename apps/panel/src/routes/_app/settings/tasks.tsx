import { createFileRoute } from "@tanstack/react-router";

import { TaskSettingsPage } from "../../../components/settings/task-settings";

export const Route = createFileRoute("/_app/settings/tasks")({
  component: TaskSettingsPage,
});
