import { createFileRoute } from "@tanstack/react-router";
import { Inbox } from "lucide-react";

import { SectionPage } from "../../components/section-page";

export const Route = createFileRoute("/_app/inbox")({
  component: InboxPage,
});

function InboxPage() {
  return <SectionPage section="inbox" icon={Inbox} actionTo="/settings" />;
}
