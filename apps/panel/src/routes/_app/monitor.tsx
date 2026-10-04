import { createFileRoute } from "@tanstack/react-router";
import { Activity } from "lucide-react";

import { SectionPage } from "../../components/section-page";

export const Route = createFileRoute("/_app/monitor")({
  component: MonitorPage,
});

function MonitorPage() {
  return <SectionPage section="monitor" icon={Activity} actionTo="/settings" />;
}
