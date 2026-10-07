import type { Conversation, Topic } from "@tolkovo/api-client";
import { useConversationList, useInboxCounts, useSession } from "@tolkovo/api-client/react";
import { Link } from "@tanstack/react-router";
import { Button, Skeleton, cn } from "@tolkovo/ui";
import { Paperclip } from "lucide-react";
import { useTranslation } from "react-i18next";

import {
  INBOX_TABS,
  formatListTime,
  tabCount,
  tabFilter,
  waitsForPerson,
  type InboxTab,
} from "../../lib/inbox/tabs";
import { CollaboratorMarks } from "./presence";
import { TopicFilter, TopicPill, useTopicMap } from "./topics";

/** The server counts unread messages up to this (Conversation.unread_count). */
const MAX_UNREAD = 100;

type ConversationListProps = {
  tab: InboxTab;
  /** `none` or a topic id: the list shows only these. */
  topic?: string | undefined;
  selectedId?: string | undefined;
  className?: string;
};

/** The left column of the inbox: tabs with live counters, the topics and the list (docs/07 §4). */
export function ConversationList({ tab, topic, selectedId, className }: ConversationListProps) {
  const { t } = useTranslation();
  const { data: counts } = useInboxCounts();
  return (
    <section
      aria-label={t("inbox.listLabel")}
      className={cn("flex min-h-0 min-w-0 flex-col border-r border-line bg-surface", className)}
    >
      <nav aria-label={t("inbox.tabs.label")} className="flex gap-0.5 overflow-x-auto border-b border-line px-3 pt-2.5">
        {INBOX_TABS.map((id) => {
          const count = tabCount(id, counts);
          return (
            <Link
              key={id}
              to="/inbox"
              search={topic ? { tab: id, topic } : { tab: id }}
              aria-current={id === tab ? "page" : undefined}
              className={cn(
                "flex items-center gap-1.5 border-b-2 border-transparent px-2.5 pt-1.5 pb-2 text-[13px] whitespace-nowrap text-ink-2 no-underline",
                "hover:text-ink aria-[current=page]:border-ink aria-[current=page]:font-medium aria-[current=page]:text-ink",
              )}
            >
              {t(`inbox.tabs.${id}`)}
              {/* A space so the accessible name reads "Мои 3", not "Мои3"; flex hides it. */}
              {count !== undefined && " "}
              {count !== undefined && (
                <b className={cn("font-mono text-[11px] font-medium tabular-nums", id === "mine" ? "text-lamp-ink" : "text-ink-3")}>
                  {count}
                </b>
              )}
            </Link>
          );
        })}
      </nav>
      <TopicFilter tab={tab} topic={topic} />
      <ListBody tab={tab} topic={topic} selectedId={selectedId} />
    </section>
  );
}

function ListBody({ tab, topic, selectedId }: { tab: InboxTab; topic?: string | undefined; selectedId?: string | undefined }) {
  const { t } = useTranslation();
  const list = useConversationList(tabFilter(tab, topic));
  const topics = useTopicMap();
  if (list.isPending) {
    return (
      <div className="flex flex-col gap-3 p-3" aria-busy="true" aria-label={t("app.loading")}>
        {[0, 1, 2, 3].map((i) => (
          <Skeleton key={i} className="h-12" />
        ))}
      </div>
    );
  }
  if (list.isError) {
    return (
      <div role="alert" className="flex flex-col items-start gap-2 p-4 text-sm text-ink-2">
        {t("inbox.loadError")}
        <Button size="sm" onClick={() => void list.refetch()}>
          {t("inbox.retry")}
        </Button>
      </div>
    );
  }
  const items = list.data.pages.flatMap((p) => p.items);
  if (items.length === 0) {
    return (
      <div className="flex flex-col gap-1 px-4 py-8 text-center">
        <p className="m-0 font-medium text-ink">{t(`inbox.empty.${tab}`)}</p>
        <p className="m-0 text-[13px] text-ink-3">{t("inbox.empty.hint")}</p>
      </div>
    );
  }
  return (
    <div className="min-h-0 flex-1 overflow-auto">
      <ul className="m-0 list-none p-0">
        {items.map((c) => (
          <li key={c.id}>
            <ConversationRow
              conversation={c}
              tab={tab}
              topic={topic}
              label={c.topic_id ? topics.get(c.topic_id) : undefined}
              selected={c.id === selectedId}
            />
          </li>
        ))}
      </ul>
      {list.hasNextPage && (
        <div className="p-3">
          <Button
            variant="ghost"
            className="w-full"
            disabled={list.isFetchingNextPage}
            onClick={() => void list.fetchNextPage()}
          >
            {t("inbox.loadMore")}
          </Button>
        </div>
      )}
    </div>
  );
}

type RowProps = {
  conversation: Conversation;
  tab: InboxTab;
  topic: string | undefined;
  /** The conversation's topic, shown before the preview. */
  label: Topic | undefined;
  selected: boolean;
};

function ConversationRow({ conversation: c, tab, topic, label, selected }: RowProps) {
  const { t, i18n } = useTranslation();
  const { data: session } = useSession();
  const wait = waitsForPerson(c, session?.user.id);
  const preview = c.last_message;
  const customerLast = preview?.sender_type === "contact";
  const finished = c.status === "resolved" || c.status === "closed";
  const unread = c.unread_count > 0;
  return (
    <Link
      to="/inbox/$conversationId"
      params={{ conversationId: c.id }}
      search={topic ? { tab, topic } : { tab }}
      aria-current={selected ? "page" : undefined}
      className={cn(
        "relative grid grid-cols-[minmax(0,1fr)_auto] gap-x-2 gap-y-0.5 border-b border-line-2 py-2.5 pr-3 pl-5 text-ink no-underline",
        "hover:bg-hover aria-[current=page]:bg-sel",
      )}
    >
      {wait && (
        <span
          aria-hidden="true"
          className="absolute top-4 left-2 size-1.5 rounded-full bg-lamp-glow shadow-[0_0_0_3px_color-mix(in_srgb,var(--lamp-glow)_25%,transparent),0_0_8px_var(--lamp-glow)]"
        />
      )}
      <span className="flex min-w-0 items-center gap-1.5">
        <span className={cn("truncate", unread ? "font-semibold" : "font-medium")}>{c.contact.name}</span>
        <span className="font-mono text-[10px] text-ink-3">{t("inbox.number", { number: c.number })}</span>
      </span>
      <span className="flex items-center gap-1.5 justify-self-end">
        <CollaboratorMarks conversationId={c.id} />
        {unread && (
          <span className="rounded-full bg-teal px-1.5 font-mono text-[10.5px] leading-[17px] font-medium text-teal-ink tabular-nums">
            <span aria-hidden="true">{c.unread_count >= MAX_UNREAD ? "99+" : c.unread_count}</span>
            <span className="sr-only">{t("inbox.unread", { count: c.unread_count })}</span>
          </span>
        )}
        <time dateTime={c.last_message_at} className="text-xs whitespace-nowrap text-ink-3">
          {formatListTime(c.last_message_at, i18n.language)}
        </time>
      </span>
      <span className="col-span-2 flex h-5 min-w-0 items-center gap-1.5 text-[13px] text-ink-2">
        {finished ? (
          <span className="rounded-sm bg-sunk px-1.5 py-0.5 font-mono text-[10px] text-ink-2">
            {t(`inbox.status.${c.status}`)}
          </span>
        ) : (
          preview && (
            <span
              title={t(customerLast ? "inbox.turn.yours" : "inbox.turn.customer")}
              className={cn("w-4 shrink-0 text-center text-xs", customerLast ? "text-lamp-ink" : "text-ink-3")}
            >
              <span aria-hidden="true">{customerLast ? "↩" : "⏸"}</span>
              <span className="sr-only">{t(customerLast ? "inbox.turn.yours" : "inbox.turn.customer")}</span>
            </span>
          )
        )}
        {label && <TopicPill topic={label} />}
        {c.awaiting_fix && !finished && (
          <span className="shrink-0 rounded-sm bg-lamp-soft px-1.5 py-0.5 text-[10.5px] font-medium text-lamp-ink">
            {t("inbox.awaitingFix")}
          </span>
        )}
        <span className="truncate">
          {preview ? <PreviewText text={preview.text} files={preview.attachments} /> : t("inbox.noPreview")}
        </span>
      </span>
    </Link>
  );
}

function PreviewText({ text, files }: { text: string; files: number }) {
  const { t } = useTranslation();
  if (text) return <>{text}</>;
  // A sticker, a location: what the feed explains in full.
  if (files === 0) return <>{t("inbox.noText")}</>;
  return (
    <span className="inline-flex items-center gap-1">
      <Paperclip aria-hidden="true" className="size-3.5" />
      {t("inbox.files", { count: files })}
    </span>
  );
}
