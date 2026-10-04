import type { Conversation, ConversationFilter, ConversationPage } from "@tolkovo/api-client";
import { queryKeys } from "@tolkovo/api-client/react";
import type { InfiniteData, QueryClient, QueryKey } from "@tanstack/react-query";

/**
 * Patches the inbox lists in the Query cache with one changed conversation instead of
 * refetching every list (docs/06 I-01). The server order is the source of truth: newest
 * activity first, then id; the patch keeps it and never invents rows past what is loaded.
 */

type Pages = InfiniteData<ConversationPage, string | undefined>;

/** Whether the conversation belongs in a list with this filter, as the server would decide. */
export function matchesFilter(filter: ConversationFilter, c: Conversation, meId: string | undefined): boolean {
  if (filter.status?.length && !filter.status.includes(c.status)) return false;
  if (filter.assignee === "none" && c.assignee_id !== null) return false;
  if (filter.assignee === "me" && (meId === undefined || c.assignee_id !== meId)) return false;
  return true;
}

/** Server order: last_message_at descending, then id descending. */
export function compareConversations(a: Conversation, b: Conversation): number {
  const byTime = Date.parse(b.last_message_at) - Date.parse(a.last_message_at);
  if (byTime !== 0) return byTime;
  return a.id < b.id ? 1 : a.id > b.id ? -1 : 0;
}

/** The pages without the conversation; the same object when it was not there. */
export function removeFromPages(data: Pages, id: string): Pages {
  if (!data.pages.some((p) => p.items.some((c) => c.id === id))) return data;
  return { ...data, pages: data.pages.map((p) => ({ ...p, items: p.items.filter((c) => c.id !== id) })) };
}

/**
 * Puts the conversation where the server would list it. A row that sorts after everything
 * loaded while more pages exist is left out: it arrives with the next page, and inserting it
 * at the end would show it twice once that page loads.
 */
export function upsertIntoPages(data: Pages, c: Conversation): Pages {
  const pages = removeFromPages(data, c.id).pages.map((p) => ({ ...p, items: [...p.items] }));
  for (const page of pages) {
    const at = page.items.findIndex((other) => compareConversations(c, other) < 0);
    if (at !== -1) {
      page.items.splice(at, 0, c);
      return { ...data, pages };
    }
  }
  const last = pages[pages.length - 1];
  if (!last || last.next_cursor !== null) return { ...data, pages };
  last.items.push(c);
  return { ...data, pages };
}

function listFilter(key: QueryKey): ConversationFilter | undefined {
  return key[0] === "conversations" && key[1] === "list" ? (key[2] as ConversationFilter) : undefined;
}

/** Applies a fresh copy of the conversation to every cached list and to its detail query. */
export function applyConversation(queryClient: QueryClient, c: Conversation, meId: string | undefined): void {
  queryClient.setQueryData(queryKeys.conversation(c.id), c);
  for (const query of queryClient.getQueryCache().findAll({ queryKey: queryKeys.conversations })) {
    const filter = listFilter(query.queryKey);
    if (!filter || query.state.data === undefined) continue;
    queryClient.setQueryData<Pages>(query.queryKey, (data) => {
      if (!data) return data;
      return matchesFilter(filter, c, meId) ? upsertIntoPages(data, c) : removeFromPages(data, c.id);
    });
  }
}

/** The conversation is gone for this user (deleted, or no longer visible). */
export function dropConversation(queryClient: QueryClient, id: string): void {
  queryClient.removeQueries({ queryKey: queryKeys.conversation(id), exact: true });
  for (const query of queryClient.getQueryCache().findAll({ queryKey: queryKeys.conversations })) {
    if (!listFilter(query.queryKey) || query.state.data === undefined) continue;
    queryClient.setQueryData<Pages>(query.queryKey, (data) => (data ? removeFromPages(data, id) : data));
  }
}

/** Keys of cached conversation queries (lists and details) that show this contact. */
export function queriesShowingContact(queryClient: QueryClient, contactId: string): QueryKey[] {
  const keys: QueryKey[] = [];
  for (const query of queryClient.getQueryCache().findAll({ queryKey: queryKeys.conversations })) {
    const data = query.state.data as Pages | Conversation | undefined;
    if (!data) continue;
    const shows =
      "pages" in data
        ? data.pages.some((p) => p.items.some((c) => c.contact.id === contactId))
        : "contact" in data && data.contact.id === contactId;
    if (shows) keys.push(query.queryKey);
  }
  return keys;
}
