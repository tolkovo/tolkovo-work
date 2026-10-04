import { useInboxCounts } from "@tolkovo/api-client/react";
import { Link, createFileRoute } from "@tanstack/react-router";
import { Button } from "@tolkovo/ui";
import { Inbox, MessagesSquare } from "lucide-react";
import { useTranslation } from "react-i18next";

import { EmptyState } from "../../../components/empty-state";

export const Route = createFileRoute("/_app/inbox/")({
  component: PickConversation,
});

/** The center of the inbox before a conversation is open; nothing open at all: connect a channel. */
function PickConversation() {
  const { t } = useTranslation();
  const { data: counts } = useInboxCounts();
  if (counts?.open === 0) {
    return (
      <div className="m-auto">
        <EmptyState
          icon={Inbox}
          title={t("pages.inbox.empty.title")}
          text={t("pages.inbox.empty.text")}
          action={
            <Button asChild variant="primary" size="lg">
              <Link to="/settings">{t("pages.inbox.empty.action")}</Link>
            </Button>
          }
        />
      </div>
    );
  }
  return (
    <div className="m-auto flex max-w-[360px] flex-col items-center gap-2 p-6 text-center">
      <span className="grid size-12 place-items-center rounded-full bg-sunk text-ink-2">
        <MessagesSquare aria-hidden="true" className="size-6" strokeWidth={1.7} />
      </span>
      <h2 className="m-0 text-base font-semibold">{t("inbox.pick.title")}</h2>
      <p className="m-0 text-ink-2">{t("inbox.pick.text")}</p>
    </div>
  );
}
