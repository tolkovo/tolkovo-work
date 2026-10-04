import { createFileRoute } from "@tanstack/react-router";
import { ChartColumn } from "lucide-react";

import { SectionPage } from "../../components/section-page";

export const Route = createFileRoute("/_app/analytics")({
  component: AnalyticsPage,
});

function AnalyticsPage() {
  return <SectionPage section="analytics" icon={ChartColumn} actionTo="/inbox" />;
}
