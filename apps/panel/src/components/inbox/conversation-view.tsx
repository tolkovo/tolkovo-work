import { isApiError, type Conversation, type Message, type MessageEvent } from "@tolkovo/api-client";
import { useConversation, useMessages, useSession } from "@tolkovo/api-client/react";
import { Link } from "@tanstack/react-router";
import { Button, Skeleton, cn } from "@tolkovo/ui";
import { AlertCircle, Check, CheckCheck, ChevronLeft, Clock, UserRound } from "lucide-react";
import { useCallback, useEffect, useLayoutEffect, useRef, useState, type ReactNode, type RefObject } from "react";
import { useTranslation } from "react-i18next";

import { useAbilities, useConversationChange, useMarkRead, useRetryDelivery } from "../../lib/inbox/actions";
import type { InboxTab } from "../../lib/inbox/tabs";
import { useViewingSignal } from "../../lib/realtime/signals";
import { MessageAttachments, ReportDetails } from "./attachments";
import { Composer } from "./composer";
import { ActionErrorLine, ConversationActions } from "./conversation-actions";
import { CUSTOMER_CARD_ID, CustomerCard } from "./customer-card";
import { IssueEventText, IssueForm, useIssueMenuItems, type IssueMode } from "./issues";
import { PresenceLine, useMemberNames } from "./presence";
import { ConversationTags } from "./tags";

type ConversationViewProps = { conversationId: string; tab: InboxTab };

/**
 * The open conversation (docs/07 §4): header with actions, tags, the feed with who else is
 * here, and the composer. Colleagues see that this operator has it open while it is mounted.
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
  return <Loaded conversation={conversation.data} tab={tab} />;
}

/** Wide enough for the customer card as a column (--breakpoint-wide, docs/07 §4). */
const WIDE = "(min-width: 77.5rem)";

function Loaded({ conversation: c, tab }: { conversation: Conversation; tab: InboxTab }) {
  const { t } = useTranslation();
  const change = useConversationChange(c.id);
  // Bumped by the composer's "show it": the feed scrolls to the newest message.
  const [latestRequest, setLatestRequest] = useState(0);
  const cardToggle = useRef<HTMLButtonElement>(null);
  const cardHeading = useRef<HTMLHeadingElement>(null);
  const card = useCardDrawer(cardToggle, cardHeading);
  // The issue form takes the place of the feed while it is open (GH-02).
  const [issueMode, setIssueMode] = useState<IssueMode | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const issueItems = useIssueMenuItems(c, (mode) => {
    setNotice(null);
    setIssueMode(mode);
  });
  return (
    <>
      <section aria-labelledby="conversation-title" className="flex min-h-0 min-w-0 flex-1 flex-col">
        <Header conversation={c} tab={tab}>
          <ConversationActions conversation={c} change={change} extra={issueItems} />
          <button
            ref={cardToggle}
            type="button"
            aria-label={t("inbox.card.toggle")}
            aria-expanded={card.open}
            aria-controls={CUSTOMER_CARD_ID}
            onClick={card.toggle}
            className="grid size-8 shrink-0 place-items-center rounded-md text-ink-2 hover:bg-hover aria-expanded:bg-sel wide:hidden"
          >
            <UserRound aria-hidden="true" className="size-4" />
          </button>
        </Header>
        {change.isError && <ActionErrorLine error={change.error} onDismiss={() => change.reset()} />}
        <NoticeLine message={notice} onDismiss={() => setNotice(null)} />
        <ConversationTags conversation={c} change={change} />
        {issueMode ? (
          <IssueForm
            conversation={c}
            mode={issueMode}
            onMode={setIssueMode}
            onClose={() => setIssueMode(null)}
            onDone={(message) => {
              setIssueMode(null);
              setNotice(message);
            }}
          />
        ) : (
          <>
            <Feed conversation={c} latestRequest={latestRequest} />
            <Composer conversation={c} onShowLatest={() => setLatestRequest((n) => n + 1)} />
          </>
        )}
      </section>
      <CustomerCard conversation={c} tab={tab} open={card.open} onClose={card.close} headingRef={cardHeading} />
    </>
  );
}

/** The outcome of an action that has no place of its own, e.g. "the issue is being created". */
function NoticeLine({ message, onDismiss }: { message: string | null; onDismiss: () => void }) {
  const { t } = useTranslation();
  return (
    <div role="status" className={message ? "flex items-center gap-2 border-b border-line bg-teal-soft px-4 py-1.5 text-[13px] text-ink" : "sr-only"}>
      {message && (
        <>
          <span className="mr-auto">{message}</span>
          <Button size="sm" variant="ghost" onClick={onDismiss}>
            {t("inbox.actions.dismiss")}
          </Button>
        </>
      )}
    </div>
  );
}

/**
 * The customer card below 1240 px: a drawer opened by the header button or Ctrl+. (docs/07 §4).
 * Opening moves focus into the card, closing returns it to the button. Wider, the card is always
 * there and Ctrl+. only moves focus to it.
 */
function useCardDrawer(toggleRef: RefObject<HTMLButtonElement | null>, headingRef: RefObject<HTMLHeadingElement | null>) {
  const [open, setOpen] = useState(false);

  const close = useCallback(() => {
    setOpen(false);
    toggleRef.current?.focus();
  }, [toggleRef]);
  const show = useCallback(() => {
    if (!window.matchMedia?.(WIDE).matches) setOpen(true);
    // After the drawer renders.
    requestAnimationFrame(() => headingRef.current?.focus());
  }, [headingRef]);
  const toggle = useCallback(() => (open ? close() : show()), [open, close, show]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.ctrlKey && !e.altKey && !e.metaKey && e.key === ".") {
        e.preventDefault();
        toggle();
      }
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [toggle]);

  return { open, toggle, close };
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

function Header({ conversation: c, tab, children }: { conversation: Conversation; tab: InboxTab; children: ReactNode }) {
  const { t } = useTranslation();
  const { data: session } = useSession();
  const names = useMemberNames();
  const assignee = c.assignee_id
    ? c.assignee_id === session?.user.id
      ? `${names.get(c.assignee_id) ?? ""} (${t("inbox.you")})`.trim()
      : (names.get(c.assignee_id) ?? t("inbox.colleague"))
    : null;
  return (
    // Wraps on a phone: the actions move under the name instead of squeezing it.
    <header className="flex min-h-[58px] flex-wrap items-center gap-x-3 gap-y-2 border-b border-line bg-surface px-4 py-2.5 max-nav:px-3">
      <BackLink tab={tab} />
      <div className="min-w-[min(15rem,70%)] flex-1">
        <h2 id="conversation-title" className="m-0 flex items-center gap-2 text-[15px] font-semibold">
          <span className="truncate">{c.contact.name}</span>
          <span className="rounded-sm border border-line px-1.5 py-0.5 font-mono text-[10px] font-medium text-ink-3">
            {t("inbox.number", { number: c.number })}
          </span>
        </h2>
        <p className="m-0 mt-0.5 flex flex-wrap gap-x-2.5 text-xs text-ink-3">
          <span>{t(`inbox.status.${c.status}`)}</span>
          {c.awaiting_fix && <span className="font-medium text-ink-2">{t("inbox.awaitingFix")}</span>}
          <span>{assignee ? t("inbox.assignee", { name: assignee }) : t("inbox.unassigned")}</span>
          {c.subject && <span className="truncate">{c.subject}</span>}
        </p>
      </div>
      {children}
    </header>
  );
}

/** How close to the bottom (px) still counts as "reading the latest". */
// The presence line sits at the end of the feed, as typing does in the prototype (docs/07 §4).
const STICK_TO_BOTTOM_PX = 80;

function Feed({ conversation, latestRequest }: { conversation: Conversation; latestRequest: number }) {
  const { t, i18n } = useTranslation();
  const messages = useMessages(conversation.id);
  const scroller = useRef<HTMLDivElement>(null);
  const atBottom = useRef(true);
  const markRead = useMarkRead(conversation);
  // Unread when the conversation was opened: "new messages" stays where it was while the
  // operator reads (reading moves the mark) and never appears for what came in before their eyes.
  const [opened] = useState(() => ({ mark: conversation.last_read_message_id, unread: conversation.unread_count }));
  // Pages and their items are newest first; the feed reads top to bottom.
  const items = messages.data ? messages.data.pages.flatMap((p) => p.items).reverse() : [];
  const newest = messages.data?.pages[0]?.items[0]?.id;
  const firstNew =
    opened.unread > 0
      ? items.find((m) => m.sender_type === "contact" && (opened.mark === null || m.id > opened.mark))?.id
      : undefined;

  // Seen means: the tab is visible and the operator is at the latest message.
  const markIfSeen = useCallback(() => {
    if (newest && atBottom.current && document.visibilityState === "visible") markRead(newest);
  }, [newest, markRead]);

  // A new message keeps the feed at the bottom only if the operator was reading the latest.
  useLayoutEffect(() => {
    const el = scroller.current;
    if (el && atBottom.current) el.scrollTop = el.scrollHeight;
    markIfSeen();
  }, [newest, markIfSeen]);
  useLayoutEffect(() => {
    const el = scroller.current;
    if (!el || latestRequest === 0) return;
    el.scrollTop = el.scrollHeight;
    atBottom.current = true;
    markIfSeen();
  }, [latestRequest, markIfSeen]);
  useEffect(() => {
    document.addEventListener("visibilitychange", markIfSeen);
    return () => document.removeEventListener("visibilitychange", markIfSeen);
  }, [markIfSeen]);

  const onScroll = () => {
    const el = scroller.current;
    if (!el) return;
    atBottom.current = el.scrollHeight - el.scrollTop - el.clientHeight < STICK_TO_BOTTOM_PX;
    markIfSeen();
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
        m.id === firstNew && m.id !== items[0]?.id ? <NewMessagesLine key="new" /> : null,
        <MessageItem key={m.id} message={m} conversation={conversation} />,
      ])}
      <PresenceLine conversationId={conversation.id} />
    </div>
  );
}

/** Where the unread part began when the operator opened the conversation (docs/07 §4). */
function NewMessagesLine() {
  const { t } = useTranslation();
  return (
    <div
      role="separator"
      aria-label={t("inbox.feed.new")}
      className="flex w-full items-center gap-2 self-center text-xs font-medium text-lamp-ink before:h-px before:flex-1 before:bg-lamp after:h-px after:flex-1 after:bg-lamp"
    >
      {t("inbox.feed.new")}
    </div>
  );
}

function SystemLine({ text }: { text: ReactNode }) {
  return (
    <div className="flex w-full items-center gap-2 self-center text-xs text-ink-3 before:h-px before:flex-1 before:bg-line after:h-px after:flex-1 after:bg-line">
      <span className="max-w-[80%] text-center">{text}</span>
    </div>
  );
}

function MessageItem({ message: m, conversation }: { message: Message; conversation: Conversation }) {
  const { t, i18n } = useTranslation();
  const contactName = conversation.contact.name;
  const names = useMemberNames();
  const time = new Intl.DateTimeFormat(i18n.language, { hour: "2-digit", minute: "2-digit" }).format(
    new Date(m.created_at),
  );
  if (m.kind === "event" || m.sender_type === "system") {
    const text = m.event ? <EventText event={m.event} actorId={m.sender_id} /> : m.body;
    return (
      <SystemLine
        text={
          <>
            {text} · <time dateTime={m.created_at}>{time}</time>
          </>
        }
      />
    );
  }

  const operatorName = m.sent_outside_panel
    ? t("inbox.sender.outsidePanel")
    : m.sender_id
      ? (names.get(m.sender_id) ?? t("inbox.sender.operator"))
      : t("inbox.sender.operator");
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
        {m.report && <ReportDetails report={m.report} />}
        {m.body}
        {m.unsupported ? <span className="text-[13px] text-ink-2 italic">{t(`inbox.unsupported.${m.unsupported}`)}</span> : null}
        <MessageAttachments message={m} />
      </div>
      {!note && outgoing && !m.sent_outside_panel && m.delivery_status && (
        <Delivery message={m} conversation={conversation} />
      )}
    </article>
  );
}

const DELIVERY_ICONS = { pending: Clock, sent: Check, delivered: Check, read: CheckCheck } as const;

/**
 * How far a reply got through its channel (docs/07 §4): ⏳ while it is on its way, ✓ sent, ✓✓
 * read (personal Telegram accounts tell), or a red line with the reason and "Повторить".
 */
function Delivery({ message: m, conversation }: { message: Message; conversation: Conversation }) {
  const { t } = useTranslation();
  const can = useAbilities(conversation);
  const retry = useRetryDelivery(conversation.id);
  if (m.delivery_status === "failed") {
    return (
      <div role="status" className="flex max-w-full flex-wrap items-center justify-end gap-x-2 gap-y-1 text-xs text-danger">
        <span className="flex min-w-0 items-start gap-1">
          <AlertCircle aria-hidden="true" className="mt-px size-3.5 shrink-0" />
          <span className="min-w-0">
            {t("inbox.delivery.failed")}: {t(`inbox.delivery.error.${m.delivery_error ?? "failed"}`)}
          </span>
        </span>
        {can.reply && (
          <Button
            size="sm"
            variant="ghost"
            className="h-6 px-2 text-xs"
            disabled={retry.isPending}
            onClick={() => retry.mutate(m.id)}
          >
            {t("inbox.delivery.retry")}
          </Button>
        )}
        {retry.isError && <span className="w-full text-right">{t("inbox.delivery.retryError")}</span>}
      </div>
    );
  }
  const status = m.delivery_status as keyof typeof DELIVERY_ICONS;
  const Icon = DELIVERY_ICONS[status] ?? Check;
  return (
    <p className="m-0 flex items-center gap-1 text-[11px] text-ink-3">
      <Icon aria-hidden="true" className={cn("size-3.5", status === "read" && "text-teal")} />
      {t(`inbox.delivery.${status in DELIVERY_ICONS ? status : "sent"}`)}
    </p>
  );
}

/**
 * A system line in the reader's language, from ids (MessageEvent in api/openapi.yaml). The
 * wording avoids gendered verbs: «Взят в работу: Анна», not «Анна взяла».
 */
function EventText({ event: e, actorId }: { event: MessageEvent; actorId: string | null }) {
  const { t } = useTranslation();
  const names = useMemberNames();
  const name = (id: string | null) => (id ? (names.get(id) ?? t("inbox.colleague")) : t("inbox.sender.system"));
  const by = actorId ? ` · ${name(actorId)}` : "";
  if (e.type.startsWith("issue_")) return <IssueEventText event={e} by={by} />;
  if (e.type === "assigned") {
    if (e.assignee_id === null) return <>{t("inbox.event.released") + by}</>;
    if (e.previous_assignee_id === null) {
      return e.assignee_id === actorId ? (
        <>{t("inbox.event.taken", { name: name(e.assignee_id) })}</>
      ) : (
        <>{t("inbox.event.assigned", { name: name(e.assignee_id) }) + by}</>
      );
    }
    const transfer = t("inbox.event.transferred", { from: name(e.previous_assignee_id), to: name(e.assignee_id) });
    return <>{transfer + (actorId === e.previous_assignee_id ? "" : by)}</>;
  }
  if (e.to === "assigned" && e.from === "resolved") return <>{t("inbox.event.reopened") + by}</>;
  if (e.to === "assigned" && e.from === "pending") return <>{t("inbox.event.resumed") + by}</>;
  return <>{t(`inbox.event.status.${e.to}`) + by}</>;
}
