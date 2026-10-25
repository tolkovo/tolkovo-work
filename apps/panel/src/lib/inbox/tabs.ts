import type { Conversation, ConversationFilter, ConversationStatus, InboxCounts } from "@tolkovo/api-client";

/** Inbox tabs of phase 1 (docs/07 §4); «У ИИ» and «Отложенные» arrive with the AI and snooze. */
export const INBOX_TABS = ["mine", "queue", "all"] as const;
export type InboxTab = (typeof INBOX_TABS)[number];

/** Not finished yet: what «Мои» and the counters look at (InboxCounts in api/openapi.yaml). */
export const OPEN_STATUSES: ConversationStatus[] = ["new", "bot", "queued", "assigned", "pending"];

/**
 * What narrows every tab: a topic (`none` or a topic id) and a line (`first` or a line id). The
 * tabs, the rows and the chips keep it in the search of /inbox.
 */
export type InboxScope = { topic?: string | undefined; line?: string | undefined };

/** The search of an inbox link: the tab with the scope, without empty parts. */
export function inboxSearch(tab: InboxTab, scope: InboxScope): { tab: InboxTab; topic?: string; line?: string } {
  return { tab, ...(scope.topic ? { topic: scope.topic } : {}), ...(scope.line ? { line: scope.line } : {}) };
}

/**
 * The list filter of a tab, narrowed to the scope. The server counts the same sets
 * (GET /conversations/counts), without the scope.
 */
export function tabFilter(tab: InboxTab, scope: InboxScope = {}): ConversationFilter {
  const narrow = { ...(scope.topic ? { topic: scope.topic } : {}), ...(scope.line ? { line: scope.line } : {}) };
  switch (tab) {
    case "mine":
      return { assignee: "me", status: ["assigned", "pending"], ...narrow };
    case "queue":
      return { assignee: "none", status: ["queued"], ...narrow };
    case "all":
      return narrow;
  }
}

/** The counter shown on a tab; «Все» has none, as in the prototype. */
export function tabCount(tab: InboxTab, counts: InboxCounts | undefined): number | undefined {
  if (!counts) return undefined;
  if (tab === "mine") return counts.mine;
  if (tab === "queue") return counts.unassigned;
  return undefined;
}

/**
 * `?tab=`, `?topic=` and `?line=` of /inbox. An absent or unknown tab means «Мои», so plain links
 * to /inbox stay valid; the topic is `none` or a topic id, the line `first` or a line id, anything
 * else is dropped.
 */
export function validateInboxSearch(search: Record<string, unknown>): {
  tab?: InboxTab | undefined;
  topic?: string | undefined;
  line?: string | undefined;
} {
  const tab = search.tab;
  const topic = typeof search.topic === "string" && /^(none|[0-9a-f-]{36})$/.test(search.topic) ? search.topic : undefined;
  const line = typeof search.line === "string" && /^(first|[0-9a-f-]{36})$/.test(search.line) ? search.line : undefined;
  return { tab: (INBOX_TABS as readonly unknown[]).includes(tab) ? (tab as InboxTab) : undefined, topic, line };
}

/**
 * Whose move it is (docs/07 §4): the customer wrote last in a conversation the operator holds,
 * or it waits in the queue for anyone. That lights the lamp.
 */
export function waitsForPerson(c: Conversation, meId: string | undefined): boolean {
  if (c.status === "queued") return true;
  return c.status === "assigned" && c.assignee_id === meId && c.last_message?.sender_type === "contact";
}

/** Clock time for today, a short date otherwise. */
export function formatListTime(iso: string, locale: string, now = new Date()): string {
  const at = new Date(iso);
  const sameDay = at.toDateString() === now.toDateString();
  return new Intl.DateTimeFormat(
    locale,
    sameDay ? { hour: "2-digit", minute: "2-digit" } : { day: "numeric", month: "short" },
  ).format(at);
}

/** Up to two letters of a name for an avatar. */
export function initials(name: string): string {
  const parts = name.trim().split(/\s+/).filter(Boolean);
  const letters = parts.length > 1 ? [parts[0], parts[parts.length - 1]] : parts;
  return letters.map((p) => [...(p ?? "")][0]?.toUpperCase() ?? "").join("") || "?";
}
