import { createFileRoute } from "@tanstack/react-router";
import { BookOpen } from "lucide-react";

import { SectionPage } from "../../components/section-page";

export const Route = createFileRoute("/_app/kb")({
  component: KnowledgeBasePage,
});

function KnowledgeBasePage() {
  return <SectionPage section="kb" icon={BookOpen} actionTo="/settings" />;
}
