import { fetchConversation, fetchTask, isApiError, type ApiClient, type Conversation, type Session, type Task } from "@tolkovo/api-client";
import { queryKeys } from "@tolkovo/api-client/react";
import type { QueryClient, QueryKey } from "@tanstack/react-query";

import { applyConversation, dropConversation, queriesShowingContact } from "../inbox/cache";
import type { RealtimeEvent } from "./protocol";
import { clearTyping } from "./signals";
import { STALE_EVENT_MS } from "../notifications/notifier";

/** A burst of events (a replay, a bulk action) becomes one request per conversation. */
export const SYNC_BATCH_MS = 50;

type InboxSyncOptions = {
  queryClient: QueryClient;
  api: ApiClient;
  batchMs?: number;
  /**
   * Hears a conversation after it was fetched for a batch of events, with those events: the
   * notifications decide on fresh state (I-05). Not called for read marks and tags.
   */
  onConversation?: (events: RealtimeEvent[], c: Conversation, meId: string | undefined) => void;
  /** Hears a task someone else just gave to the signed-in member, freshly fetched (ADR-0015). */
  onTaskAssigned?: (task: Task) => void;
};

const str = (v: unknown): string | undefined => (typeof v === "string" ? v : undefined);

/**
 * Turns domain events into precise cache updates (docs/02 §10.4, docs/06 I-01). Event payloads
 * carry ids only, so a changed conversation is fetched once and patched into every cached list;
 * the counters and an open feed are refetched. Everything else is invalidated as before.
 */
export function createInboxSync({ queryClient, api, batchMs = SYNC_BATCH_MS, onConversation, onTaskAssigned }: InboxSyncOptions) {
  const conversations = new Set<string>();
  /** Events of each conversation in the current batch, for onConversation. */
  const batchEvents = new Map<string, RealtimeEvent[]>();
  const feeds = new Set<string>();
  const contacts = new Set<string>();
  const roots = new Map<string, QueryKey>();
  let counts = false;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let stopped = false;
  /** Highest version applied per conversation: an older answer that arrives late is dropped. */
  const applied = new Map<string, number>();

  const meId = () => queryClient.getQueryData<Session | null>(queryKeys.session)?.user.id;

  const remember = (id: string, event: RealtimeEvent) => {
    if (!onConversation) return;
    const list = batchEvents.get(id);
    if (list) list.push(event);
    else batchEvents.set(id, [event]);
  };

  const schedule = () => {
    if (timer === undefined && !stopped) timer = setTimeout(() => void flush(), batchMs);
  };

  const apply = (c: Conversation) => {
    if ((applied.get(c.id) ?? -1) > c.version) return;
    applied.set(c.id, c.version);
    applyConversation(queryClient, c, meId());
  };

  const refresh = async (id: string, events: RealtimeEvent[]) => {
    try {
      const c = await fetchConversation(api, id);
      if (stopped) return;
      apply(c);
      if (events.length > 0) onConversation?.(events, c, meId());
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
    const events = new Map(batchEvents);
    batchEvents.clear();
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
    await Promise.all(ids.map((id) => refresh(id, events.get(id) ?? [])));
  }

  return {
    onEvent(event: RealtimeEvent) {
      const conversationId = str(event.data.conversation_id);
      if (event.type === "conversation.read") {
        // Read marks are per member: only the reader's own tabs refresh their unread counters.
        if (!conversationId || str(event.data.reader_id) !== meId()) return;
        conversations.add(conversationId);
        counts = true; // mine_unread
      } else if ((event.type === "conversation.tags_changed" || event.type === "conversation.rated") && conversationId) {
        // The customer's rating is on the conversation itself.
        conversations.add(conversationId);
      } else if (event.type === "conversation.issues_changed" && conversationId) {
        // A GitHub issue was created, linked, refused or removed: its list and awaiting_fix.
        conversations.add(conversationId);
        roots.set(`issues:${conversationId}`, queryKeys.conversationIssues(conversationId));
        roots.set("open-issues", queryKeys.openGitHubIssues);
      } else if (event.type === "conversation.tasks_changed" && conversationId) {
        // A task was linked, unlinked, closed or reopened: its list, awaiting_fix and the reply draft.
        conversations.add(conversationId);
        roots.set(`tasks:${conversationId}`, queryKeys.conversationTasks(conversationId));
      } else if (event.type.startsWith("conversation.") && conversationId) {
        conversations.add(conversationId);
        remember(conversationId, event);
        counts = true;
      } else if (event.type === "message.created" && conversationId) {
        conversations.add(conversationId);
        feeds.add(conversationId);
        remember(conversationId, event);
        const sender = str(event.data.sender_type);
        if (sender === "contact") counts = true; // mine_unread
        if (sender === "operator" || sender === "contact" || sender === "bot") {
          clearTyping(conversationId, sender, str(event.data.sender_id) ?? null);
        }
      } else if (event.type === "message.delivery" && conversationId) {
        // A reply was sent, read or failed: the open feed shows its new mark.
        feeds.add(conversationId);
      } else if (event.type.startsWith("contact.")) {
        const contactId = str(event.data.contact_id);
        roots.set("contacts", queryKeys.contacts);
        if (contactId) contacts.add(contactId);
      } else if (event.type === "task.changed") {
        // The tasks section: lists, the open task, its feed and the counter in the rail.
        roots.set("tasks", queryKeys.tasks);
        const taskId = str(event.data.task_id);
        const me = meId();
        if (
          onTaskAssigned &&
          taskId &&
          me &&
          event.data.assigned === true &&
          str(event.data.assignee_id) === me &&
          str(event.data.actor_id) !== me &&
          Date.now() - Date.parse(event.ts) < STALE_EVENT_MS
        ) {
          void fetchTask(api, taskId).then(
            (task) => !stopped && task.assignee_id === me && onTaskAssigned(task),
            () => undefined, // deleted or unreachable meanwhile: nothing to tell
          );
        }
      } else if (event.type === "member.changed") {
        // A role changed or someone left the team (ADR-0020): the team, and for the member
        // themselves their permissions, then everything those permissions show.
        roots.set("members", queryKeys.members);
        if (str(event.data.user_id) === meId()) {
          roots.set("session", queryKeys.session);
          roots.set("conversations", queryKeys.conversations);
          roots.set("tasks", queryKeys.tasks);
        }
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
      roots.set("tasks", queryKeys.tasks);
      roots.set("members", queryKeys.members);
      schedule();
    },
    stop() {
      stopped = true;
      clearTimeout(timer);
    },
  };
}
