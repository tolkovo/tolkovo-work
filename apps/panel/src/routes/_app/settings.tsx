import { createFileRoute } from "@tanstack/react-router";
import { Settings } from "lucide-react";

import { SectionPage } from "../../components/section-page";

export const Route = createFileRoute("/_app/settings")({
  component: SettingsPage,
});

function SettingsPage() {
  return <SectionPage section="settings" icon={Settings} actionTo="/login" />;
}
