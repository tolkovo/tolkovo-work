import { createFileRoute } from "@tanstack/react-router";

import { NotificationSettingsPage } from "../../../components/settings/notification-settings";

export const Route = createFileRoute("/_app/settings/notifications")({
  component: NotificationSettingsPage,
});
