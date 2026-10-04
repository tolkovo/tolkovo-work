import type { Conversation, ConversationFilter, ConversationStatus, InboxCounts } from "@tolkovo/api-client";

/** Inbox tabs of phase 1 (docs/07 §4); «У ИИ» and «Отложенные» arrive with the AI and snooze. */
export const INBOX_TABS = ["mine", "queue", "all"] as const;
export type InboxTab = (typeof INBOX_TABS)[number];

/** Not finished yet: what «Мои» and the counters look at (InboxCounts in api/openapi.yaml). */
export const OPEN_STATUSES: ConversationStatus[] = ["new", "bot", "queued", "assigned", "pending"];

/** The list filter of a tab. The server counts the same sets (GET /conversations/counts). */
export function tabFilter(tab: InboxTab): ConversationFilter {
  switch (tab) {
    case "mine":
      return { assignee: "me", status: ["assigned", "pending"] };
    case "queue":
      return { assignee: "none", status: ["queued"] };
    case "all":
      return {};
  }
}

/** The counter shown on a tab; «Все» has none, as in the prototype. */
export function tabCount(tab: InboxTab, counts: InboxCounts | undefined): number | undefined {
  if (!counts) return undefined;
  if (tab === "mine") return counts.mine;
  if (tab === "queue") return counts.unassigned;
  return undefined;
}

/** `?tab=` of /inbox; absent or unknown means «Мои», so plain links to /inbox stay valid. */
export function validateInboxSearch(search: Record<string, unknown>): { tab?: InboxTab | undefined } {
  const tab = search.tab;
  return { tab: (INBOX_TABS as readonly unknown[]).includes(tab) ? (tab as InboxTab) : undefined };
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
