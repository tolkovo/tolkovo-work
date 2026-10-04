import { fetchConversation, isApiError, type ApiClient, type Conversation, type Session } from "@tolkovo/api-client";
import { queryKeys } from "@tolkovo/api-client/react";
import type { QueryClient, QueryKey } from "@tanstack/react-query";

import { applyConversation, dropConversation, queriesShowingContact } from "../inbox/cache";
import type { RealtimeEvent } from "./protocol";
import { clearTyping } from "./signals";

/** A burst of events (a replay, a bulk action) becomes one request per conversation. */
export const SYNC_BATCH_MS = 50;

type InboxSyncOptions = {
  queryClient: QueryClient;
  api: ApiClient;
  batchMs?: number;
};

const str = (v: unknown): string | undefined => (typeof v === "string" ? v : undefined);

/**
 * Turns domain events into precise cache updates (docs/02 §10.4, docs/06 I-01). Event payloads
 * carry ids only, so a changed conversation is fetched once and patched into every cached list;
 * the counters and an open feed are refetched. Everything else is invalidated as before.
 */
export function createInboxSync({ queryClient, api, batchMs = SYNC_BATCH_MS }: InboxSyncOptions) {
  const conversations = new Set<string>();
  const feeds = new Set<string>();
  const contacts = new Set<string>();
  const roots = new Map<string, QueryKey>();
  let counts = false;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let stopped = false;
  /** Highest version applied per conversation: an older answer that arrives late is dropped. */
  const applied = new Map<string, number>();

  const meId = () => queryClient.getQueryData<Session | null>(queryKeys.session)?.user.id;

  const schedule = () => {
    if (timer === undefined && !stopped) timer = setTimeout(() => void flush(), batchMs);
  };

  const apply = (c: Conversation) => {
    if ((applied.get(c.id) ?? -1) > c.version) return;
    applied.set(c.id, c.version);
    applyConversation(queryClient, c, meId());
  };

  const refresh = async (id: string) => {
    try {
      const c = await fetchConversation(api, id);
      if (!stopped) apply(c);
    } catch (error) {
      if (stopped) return;
      if (isApiError(error) && (error.status === 404 || error.status === 403)) dropConversation(queryClient, id);
      // Anything else (offline, a server error): let the lists refetch rather than drift.
      else void queryClient.invalidateQueries({ queryKey: queryKeys.conversations });
    }
  };

  async function flush() {
    timer = undefined;
    const ids = [...conversations];
    conversations.clear();
    for (const id of feeds) void queryClient.invalidateQueries({ queryKey: queryKeys.conversationMessages(id) });
    feeds.clear();
    for (const id of contacts) {
      for (const queryKey of queriesShowingContact(queryClient, id)) void queryClient.invalidateQueries({ queryKey });
    }
    contacts.clear();
    for (const queryKey of roots.values()) void queryClient.invalidateQueries({ queryKey });
    roots.clear();
    if (counts) void queryClient.invalidateQueries({ queryKey: queryKeys.inboxCounts });
    counts = false;
    await Promise.all(ids.map(refresh));
  }

  return {
    onEvent(event: RealtimeEvent) {
      const conversationId = str(event.data.conversation_id);
      if (event.type.startsWith("conversation.") && conversationId) {
        conversations.add(conversationId);
        counts = true;
      } else if (event.type === "message.created" && conversationId) {
        conversations.add(conversationId);
        feeds.add(conversationId);
        const sender = str(event.data.sender_type);
        if (sender === "operator" || sender === "contact" || sender === "bot") {
          clearTyping(conversationId, sender, str(event.data.sender_id) ?? null);
        }
      } else if (event.type.startsWith("contact.")) {
        const contactId = str(event.data.contact_id);
        roots.set("contacts", queryKeys.contacts);
        if (contactId) contacts.add(contactId);
      } else if (event.type.startsWith("conversation.") || event.type.startsWith("message.")) {
        // A future event of these families without the ids above: stay correct, refetch.
        roots.set("conversations", queryKeys.conversations);
      } else {
        return;
      }
      schedule();
    },
    /** Missed events cannot be replayed: everything realtime-backed is refetched. */
    onResync() {
      roots.set("conversations", queryKeys.conversations);
      roots.set("contacts", queryKeys.contacts);
      schedule();
    },
    stop() {
      stopped = true;
      clearTimeout(timer);
    },
  };
}
