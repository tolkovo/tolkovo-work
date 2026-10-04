import { isApiError, type Conversation, type Message } from "@tolkovo/api-client";
import { useConversation, useMessages, useSession } from "@tolkovo/api-client/react";
import { Link } from "@tanstack/react-router";
import { Button, Skeleton, cn } from "@tolkovo/ui";
import { ChevronLeft, Paperclip } from "lucide-react";
import { useEffect, useLayoutEffect, useRef } from "react";
import { useTranslation } from "react-i18next";

import type { InboxTab } from "../../lib/inbox/tabs";
import { useViewingSignal } from "../../lib/realtime/signals";
import { PresenceLine, useMemberNames } from "./presence";

type ConversationViewProps = { conversationId: string; tab: InboxTab };

/**
 * The open conversation (docs/07 §4): header, feed and who else is here. Colleagues see that
 * this operator has it open while the view is mounted. Replying and notes come with I-02.
 */
export function ConversationView({ conversationId, tab }: ConversationViewProps) {
  const { t } = useTranslation();
  const conversation = useConversation(conversationId);
  useViewingSignal(conversationId);

  if (conversation.isPending) {
    return (
      <div className="flex min-w-0 flex-1 flex-col gap-3 p-4" aria-busy="true" aria-label={t("app.loading")}>
        <Skeleton className="h-10 w-1/2" />
        <Skeleton className="h-24" />
      </div>
    );
  }
  if (conversation.isError) {
    const missing = isApiError(conversation.error) && [403, 404].includes(conversation.error.status);
    return (
      <div role="alert" className="flex min-w-0 flex-1 flex-col items-start gap-2 p-5 text-sm text-ink-2">
        <BackLink tab={tab} />
        {t(missing ? "inbox.feed.notFound" : "inbox.feed.error")}
        {!missing && (
          <Button size="sm" onClick={() => void conversation.refetch()}>
            {t("inbox.retry")}
          </Button>
        )}
      </div>
    );
  }
  return (
    <section aria-labelledby="conversation-title" className="flex min-h-0 min-w-0 flex-1 flex-col">
      <Header conversation={conversation.data} tab={tab} />
      <Feed conversation={conversation.data} />
    </section>
  );
}

function BackLink({ tab }: { tab: InboxTab }) {
  const { t } = useTranslation();
  return (
    <Link
      to="/inbox"
      search={{ tab }}
      aria-label={t("inbox.back")}
      className="hidden size-8 shrink-0 place-items-center rounded-md text-ink-2 hover:bg-hover max-nav:grid"
    >
      <ChevronLeft aria-hidden="true" className="size-5" />
    </Link>
  );
}

function Header({ conversation: c, tab }: { conversation: Conversation; tab: InboxTab }) {
  const { t } = useTranslation();
  const { data: session } = useSession();
  const names = useMemberNames();
  const assignee = c.assignee_id
    ? c.assignee_id === session?.user.id
      ? `${names.get(c.assignee_id) ?? ""} (${t("inbox.you")})`.trim()
      : (names.get(c.assignee_id) ?? t("inbox.colleague"))
    : null;
  return (
    <header className="flex min-h-[58px] items-center gap-3 border-b border-line bg-surface px-4 py-2.5">
      <BackLink tab={tab} />
      <div className="min-w-0 flex-1">
        <h2 id="conversation-title" className="m-0 flex items-center gap-2 text-[15px] font-semibold">
          <span className="truncate">{c.contact.name}</span>
          <span className="rounded-sm border border-line px-1.5 py-0.5 font-mono text-[10px] font-medium text-ink-3">
            {t("inbox.number", { number: c.number })}
          </span>
        </h2>
        <p className="m-0 mt-0.5 flex flex-wrap gap-x-2.5 text-xs text-ink-3">
          <span>{t(`inbox.status.${c.status}`)}</span>
          <span>{assignee ? t("inbox.assignee", { name: assignee }) : t("inbox.unassigned")}</span>
          {c.subject && <span className="truncate">{c.subject}</span>}
        </p>
      </div>
    </header>
  );
}

/** How close to the bottom (px) still counts as "reading the latest". */
// The presence line sits at the end of the feed, as typing does in the prototype (docs/07 §4).
const STICK_TO_BOTTOM_PX = 80;

function Feed({ conversation }: { conversation: Conversation }) {
  const { t, i18n } = useTranslation();
  const messages = useMessages(conversation.id);
  const scroller = useRef<HTMLDivElement>(null);
  const atBottom = useRef(true);
  // Pages and their items are newest first; the feed reads top to bottom.
  const items = messages.data ? messages.data.pages.flatMap((p) => p.items).reverse() : [];
  const newest = items.at(-1)?.id;

  // A new message keeps the feed at the bottom only if the operator was reading the latest.
  useLayoutEffect(() => {
    const el = scroller.current;
    if (el && atBottom.current) el.scrollTop = el.scrollHeight;
  }, [newest]);
  useEffect(() => {
    atBottom.current = true;
  }, [conversation.id]);

  const onScroll = () => {
    const el = scroller.current;
    if (el) atBottom.current = el.scrollHeight - el.scrollTop - el.clientHeight < STICK_TO_BOTTOM_PX;
  };

  const day = new Intl.DateTimeFormat(i18n.language, { dateStyle: "long" });
  const days = items.map((m) => day.format(new Date(m.created_at)));
  return (
    <div
      ref={scroller}
      onScroll={onScroll}
      role="log"
      aria-label={t("inbox.feed.label")}
      className="flex min-h-0 flex-1 flex-col gap-2.5 overflow-auto bg-paper px-5 pt-4 pb-2 max-nav:px-3"
    >
      {messages.hasNextPage && (
        <Button
          variant="ghost"
          size="sm"
          className="self-center"
          disabled={messages.isFetchingNextPage}
          onClick={() => void messages.fetchNextPage()}
        >
          {t("inbox.feed.older")}
        </Button>
      )}
      {messages.isPending && <Skeleton className="h-16 w-2/3" />}
      {messages.isError && (
        <p role="alert" className="m-0 text-sm text-ink-2">
          {t("inbox.feed.error")}
        </p>
      )}
      {messages.isSuccess && items.length === 0 && (
        <p className="m-auto text-sm text-ink-3">{t("inbox.feed.empty")}</p>
      )}
      {items.map((m, i) => [
        days[i] !== days[i - 1] ? <SystemLine key={`day-${days[i]}`} text={days[i] ?? ""} /> : null,
        <MessageItem key={m.id} message={m} contactName={conversation.contact.name} />,
      ])}
      <PresenceLine conversationId={conversation.id} />
    </div>
  );
}

function SystemLine({ text }: { text: string }) {
  return (
    <div className="flex w-full items-center gap-2 self-center text-xs text-ink-3 before:h-px before:flex-1 before:bg-line after:h-px after:flex-1 after:bg-line">
      {text}
    </div>
  );
}

function MessageItem({ message: m, contactName }: { message: Message; contactName: string }) {
  const { t, i18n } = useTranslation();
  const names = useMemberNames();
  const time = new Intl.DateTimeFormat(i18n.language, { hour: "2-digit", minute: "2-digit" }).format(
    new Date(m.created_at),
  );
  if (m.kind === "event" || m.sender_type === "system") return <SystemLine text={`${m.body} · ${time}`} />;

  const operatorName = m.sender_id ? (names.get(m.sender_id) ?? t("inbox.sender.operator")) : t("inbox.sender.operator");
  const by =
    m.sender_type === "contact" ? contactName : m.sender_type === "bot" ? t("inbox.sender.bot") : operatorName;
  const note = m.is_private;
  const outgoing = m.sender_type !== "contact";
  return (
    <article
      className={cn(
        "flex max-w-[min(72%,560px)] flex-col gap-[3px] max-nav:max-w-[88%]",
        note ? "max-w-none self-stretch" : outgoing ? "items-end self-end" : "self-start",
      )}
    >
      <p className={cn("m-0 text-[11.5px] text-ink-3", m.sender_type === "bot" && "text-ai")}>
        {note ? `${t("inbox.sender.note")} · ${by}` : by} · <time dateTime={m.created_at}>{time}</time>
      </p>
      <div
        className={cn(
          "rounded-xl border px-3 py-2 break-words whitespace-pre-wrap",
          note
            ? "border-note-line bg-note"
            : m.sender_type === "bot"
              ? "rounded-tr-sm border-dashed border-ai/45 bg-ai-soft"
              : outgoing
                ? "rounded-tr-sm border-transparent bg-teal-soft"
                : "rounded-tl-sm border-line bg-surface",
        )}
      >
        {m.body}
        {m.attachments.length > 0 && (
          <ul className="m-0 mt-1 flex list-none flex-col gap-0.5 p-0 text-[13px] text-ink-2">
            {m.attachments.map((a) => (
              <li key={a.id} className="flex items-center gap-1">
                <Paperclip aria-hidden="true" className="size-3.5" />
                {a.file_name}
              </li>
            ))}
          </ul>
        )}
      </div>
    </article>
  );
}
