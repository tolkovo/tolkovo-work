import { Outlet, createFileRoute, useParams } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";

import { ConversationList } from "../../components/inbox/conversation-list";
import { validateInboxSearch } from "../../lib/inbox/tabs";

export const Route = createFileRoute("/_app/inbox")({
  validateSearch: validateInboxSearch,
  component: InboxLayout,
});

/**
 * The operator inbox (docs/07 §4): the list, the open conversation and its customer card side
 * by side; below 1240 px the card is a drawer, below 760 px the list and the conversation take
 * turns.
 */
function InboxLayout() {
  const { t } = useTranslation();
  const { tab = "mine", topic } = Route.useSearch();
  const { conversationId } = useParams({ strict: false });
  return (
    <div className="grid h-full grid-cols-[344px_minmax(0,1fr)] max-nav:grid-cols-[minmax(0,1fr)]">
      <h1 className="sr-only">{t("pages.inbox.title")}</h1>
      <ConversationList
        tab={tab}
        topic={topic}
        selectedId={conversationId}
        className={conversationId ? "max-nav:hidden" : undefined}
      />
      <div className={conversationId ? "flex min-h-0 min-w-0" : "flex min-h-0 min-w-0 max-nav:hidden"}>
        <Outlet />
      </div>
    </div>
  );
}
