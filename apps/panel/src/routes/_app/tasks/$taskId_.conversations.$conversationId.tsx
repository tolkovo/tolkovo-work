import { Link, createFileRoute } from "@tanstack/react-router";
import { ChevronLeft } from "lucide-react";
import { useTranslation } from "react-i18next";

import { ConversationView } from "../../../components/inbox/conversation-view";

export const Route = createFileRoute("/_app/tasks/$taskId_/conversations/$conversationId")({
  component: LinkedConversationPage,
});

/**
 * A conversation linked to the task, opened from it in the tasks section: the way the
 * programmer reads it, who has no inbox (ADR-0020). Back leads to the task.
 */
function LinkedConversationPage() {
  const { t } = useTranslation();
  const { taskId, conversationId } = Route.useParams();
  const search = Route.useSearch();
  const back = (
    <Link
      to="/tasks/$taskId"
      params={{ taskId }}
      search={search}
      aria-label={t("tasks.backToTask")}
      className="grid size-8 shrink-0 place-items-center rounded-md text-ink-2 hover:bg-hover"
    >
      <ChevronLeft aria-hidden="true" className="size-5" />
    </Link>
  );
  return <ConversationView key={conversationId} conversationId={conversationId} tab="all" back={back} />;
}
