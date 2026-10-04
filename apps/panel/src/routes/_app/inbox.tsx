import { Outlet, createFileRoute, useParams } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";

import { ConversationList } from "../../components/inbox/conversation-list";
import { validateInboxSearch } from "../../lib/inbox/tabs";

export const Route = createFileRoute("/_app/inbox")({
  validateSearch: validateInboxSearch,
  component: InboxLayout,
});

/**
 * The operator inbox (docs/07 §4): the list and the open conversation side by side; below
 * 760 px one at a time. The customer card arrives with I-03.
 */
function InboxLayout() {
  const { t } = useTranslation();
  const { tab = "mine" } = Route.useSearch();
  const { conversationId } = useParams({ strict: false });
  return (
    <div className="grid h-full grid-cols-[344px_minmax(0,1fr)] max-nav:grid-cols-[minmax(0,1fr)]">
      <h1 className="sr-only">{t("pages.inbox.title")}</h1>
      <ConversationList tab={tab} selectedId={conversationId} className={conversationId ? "max-nav:hidden" : undefined} />
      <div className={conversationId ? "flex min-h-0 min-w-0" : "flex min-h-0 min-w-0 max-nav:hidden"}>
        <Outlet />
      </div>
    </div>
  );
}
