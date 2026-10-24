import { setConversationTopic, type Conversation, type MessageEvent, type Topic } from "@tolkovo/api-client";
import { useTopics } from "@tolkovo/api-client/react";
import { cn } from "@tolkovo/ui";
import { useNavigate } from "@tanstack/react-router";
import { useId, type ReactNode } from "react";
import { useTranslation } from "react-i18next";

import { useAbilities, type ConversationChange } from "../../lib/inbox/actions";
import { inboxSearch, type InboxScope, type InboxTab } from "../../lib/inbox/tabs";
import { TAG_DOT } from "./palette";

/**
 * Topics of questions (docs/02 §7.3): what the customer picked in the bot or the widget before
 * writing, or what an operator set. The inbox shows it on every row and filters by it, so the
 * team sees at a glance what to take first.
 */

/** The topics of the workspace by id. */
export function useTopicMap(): Map<string, Topic> {
  const { data } = useTopics();
  return new Map((data ?? []).map((topic) => [topic.id, topic]));
}

/** A small label with the topic's color, for lists. */
export function TopicPill({ topic, className }: { topic: Topic; className?: string }) {
  return (
    <span
      className={cn(
        "inline-flex h-[18px] shrink-0 items-center gap-1 rounded-full border border-line bg-surface px-1.5 text-[10.5px] font-medium text-ink-2",
        className,
      )}
    >
      <span aria-hidden="true" className={cn("size-1.5 rounded-full", TAG_DOT[topic.color])} />
      {topic.name}
    </span>
  );
}

/** The topic of the conversation under its header: a choice for who may tag, a label for others. */
export function ConversationTopic({ conversation: c, change }: { conversation: Conversation; change: ConversationChange }) {
  const { t } = useTranslation();
  const can = useAbilities(c);
  const { data: topics } = useTopics();
  const id = useId();
  const current = topics?.find((topic) => topic.id === c.topic_id);
  if (!topics || (topics.length === 0 && !c.topic_id)) return null;
  if (!can.tag) return current ? <TopicPill topic={current} /> : null;
  return (
    <span className="inline-flex items-center gap-1.5 text-xs text-ink-2">
      <label htmlFor={id}>{t("inbox.topic.label")}</label>
      <select
        id={id}
        value={c.topic_id ?? ""}
        disabled={change.isPending}
        onChange={(e) => {
          const next = e.currentTarget.value || null;
          change.mutate((api) => setConversationTopic(api, c.id, next, c.version));
        }}
        className="h-[22px] rounded-full border border-line bg-surface px-2 text-xs text-ink"
      >
        <option value="">{t("inbox.topic.none")}</option>
        {topics.map((topic) => (
          <option key={topic.id} value={topic.id}>
            {topic.name}
          </option>
        ))}
        {c.topic_id && !current && <option value={c.topic_id}>{t("inbox.topic.deleted")}</option>}
      </select>
    </span>
  );
}

/** The feed line of a topic change: «Клиент выбрал тему «CRM»», «Тема: CRM · Анна», «Тема убрана». */
export function TopicEventText({ event: e, by }: { event: MessageEvent; by: string }) {
  const { t } = useTranslation();
  const topics = useTopicMap();
  if (!e.topic_id) return <>{t("inbox.event.topicRemoved") + by}</>;
  const name = topics.get(e.topic_id)?.name ?? t("inbox.topic.deleted");
  if (e.by_customer) return <>{t("inbox.event.topicChosen", { name })}</>;
  return <>{t("inbox.event.topicSet", { name }) + by}</>;
}

/**
 * Chips over the list: all, each topic, without a topic. The choice lives in `?topic=`, so a
 * link keeps it and the tabs keep it too.
 */
export function TopicFilter({ tab, scope }: { tab: InboxTab; scope: InboxScope }) {
  const topic = scope.topic;
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { data: topics } = useTopics();
  if (!topics || topics.length === 0) return null;
  const chip = (value: string | undefined, label: ReactNode, key: string) => (
    <button
      key={key}
      type="button"
      aria-pressed={topic === value}
      onClick={() => void navigate({ to: "/inbox", search: inboxSearch(tab, { ...scope, topic: value }) })}
      className={cn(
        "inline-flex h-6 shrink-0 items-center gap-1 rounded-full border border-line bg-transparent px-2 text-xs whitespace-nowrap text-ink-2",
        "hover:bg-hover hover:text-ink aria-pressed:border-ink aria-pressed:bg-sel aria-pressed:text-ink",
      )}
    >
      {label}
    </button>
  );
  return (
    <div role="group" aria-label={t("inbox.topic.filter")} className="flex gap-1.5 overflow-x-auto border-b border-line px-3 py-2">
      {chip(undefined, t("inbox.topic.all"), "all")}
      {topics.map((x) =>
        chip(
          x.id,
          <>
            <span aria-hidden="true" className={cn("size-1.5 rounded-full", TAG_DOT[x.color])} />
            {x.name}
          </>,
          x.id,
        ),
      )}
      {chip("none", t("inbox.topic.without"), "none")}
    </div>
  );
}
