import {
  fetchConversation,
  hasPermission,
  isApiError,
  markConversationRead,
  postMessage,
  retryMessageDelivery,
  type ApiClient,
  type Conversation,
  type Message,
  type Session,
} from "@tolkovo/api-client";
import { queryKeys, useApiClient, useSession } from "@tolkovo/api-client/react";
import { useMutation, useQueryClient, type QueryClient } from "@tanstack/react-query";
import { useCallback, useRef } from "react";

import { addToFeed, applyConversation, patchConversation, replaceInFeed } from "./cache";

/**
 * What the signed-in member may do with a conversation, as the server decides it
 * (api/openapi.yaml, docs/04 §4.3). A UI hint only: hidden buttons are not a security
 * boundary, the server checks every request again (docs/00 §9.2).
 */
export type Abilities = {
  reply: boolean;
  note: boolean;
  take: boolean;
  release: boolean;
  transfer: boolean;
  close: boolean;
  /** assigned → pending */
  wait: boolean;
  /** pending → assigned */
  resume: boolean;
  /** resolved → assigned, back to the same assignee */
  reopen: boolean;
  tag: boolean;
  createTag: boolean;
};

export function abilities(session: Session | null | undefined, c: Conversation): Abilities {
  const can = (p: string) => hasPermission(session, p);
  const me = session?.user.id;
  const own = me !== undefined && c.assignee_id === me;
  const open = c.status !== "closed";
  const changeStatus = can("conversations.reply") && (own || can("conversations.manage"));
  const assignOthers = can("conversations.assign");
  const moveOwn = own && can("conversations.take");
  return {
    reply: open && can("conversations.reply") && (own || can("conversations.manage")),
    note: open && can("conversations.note"),
    take: (can("conversations.take") || assignOthers) && c.assignee_id === null && c.status === "queued",
    release: c.status === "assigned" && (moveOwn || assignOthers),
    transfer: (c.status === "assigned" || c.status === "pending") && (moveOwn || assignOthers),
    close: changeStatus && (c.status === "assigned" || c.status === "pending"),
    wait: changeStatus && c.status === "assigned",
    resume: changeStatus && c.status === "pending",
    reopen: changeStatus && c.status === "resolved" && c.assignee_id !== null,
    tag: can("conversations.tag"),
    createTag: can("tags.manage"),
  };
}

export function useAbilities(c: Conversation): Abilities {
  const { data: session } = useSession();
  return abilities(session, c);
}

const meIdFrom = (queryClient: QueryClient) =>
  queryClient.getQueryData<Session | null>(queryKeys.session)?.user.id;

/** Fetches the conversation and puts it into every cached list: after a reply or a 409. */
export async function refreshConversation(queryClient: QueryClient, api: ApiClient, id: string): Promise<void> {
  try {
    applyConversation(queryClient, await fetchConversation(api, id), meIdFrom(queryClient));
  } catch {
    void queryClient.invalidateQueries({ queryKey: queryKeys.conversation(id) });
  }
}

/**
 * A change of the conversation (take, transfer, close, tags). The answer is applied to every
 * cached list at once; the system line it wrote arrives with the feed. On `409
 * version_conflict` the conversation is refetched, so the next attempt uses the fresh version.
 */
export function useConversationChange(conversationId: string) {
  const api = useApiClient();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (change: (api: ApiClient) => Promise<Conversation>) => change(api),
    onSuccess: (c) => {
      applyConversation(queryClient, c, meIdFrom(queryClient));
      void queryClient.invalidateQueries({ queryKey: queryKeys.inboxCounts });
      void queryClient.invalidateQueries({ queryKey: queryKeys.conversationMessages(c.id) });
    },
    onError: (error) => {
      // Someone changed it first (409) or it is no longer allowed (403): show the fresh state.
      if (isApiError(error) && (error.status === 409 || error.status === 403)) {
        void refreshConversation(queryClient, api, conversationId);
      }
    },
  });
}

export type ConversationChange = ReturnType<typeof useConversationChange>;

/** Whether the error means "someone changed it first": refetch and let the operator retry. */
export function isConflict(error: unknown): boolean {
  return isApiError(error) && error.status === 409 && error.code === "version_conflict";
}

export type SendInput = { body: string; private: boolean; idempotencyKey: string };

/** Sends a reply or a note and shows it in the feed without waiting for the realtime event. */
export function useSendMessage(conversationId: string) {
  const api = useApiClient();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: SendInput): Promise<Message> =>
      postMessage(api, conversationId, { body: input.body, private: input.private }, input.idempotencyKey),
    onSuccess: (m) => {
      addToFeed(queryClient, m);
      // A reply changes the preview, the first response time and whose move it is.
      if (!m.is_private) void refreshConversation(queryClient, api, conversationId);
    },
  });
}

/**
 * Sends a reply that failed to deliver again. Whatever the answer, the feed is refetched after
 * an error: someone may have retried it first (409) or the conversation moved on.
 */
export function useRetryDelivery(conversationId: string) {
  const api = useApiClient();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (messageId: string): Promise<Message> => retryMessageDelivery(api, conversationId, messageId),
    onSuccess: (m) => replaceInFeed(queryClient, m),
    onError: () => void queryClient.invalidateQueries({ queryKey: queryKeys.conversationMessages(conversationId) }),
  });
}

/**
 * Moves the signed-in member's read mark (docs/02 §10.5). Repeated calls for the same message
 * send one request; a mark never moves back, here or on the server.
 */
export function useMarkRead(c: Conversation): (messageId: string) => void {
  const api = useApiClient();
  const queryClient = useQueryClient();
  const sent = useRef<string | null>(null);
  const { id, last_read_message_id: mark } = c;
  return useCallback(
    (messageId: string) => {
      // UUIDv7 ids sort as strings in creation order.
      if ((mark !== null && messageId <= mark) || (sent.current !== null && messageId <= sent.current)) return;
      sent.current = messageId;
      markConversationRead(api, id, messageId).then(
        () =>
          patchConversation(queryClient, id, (cur) =>
            cur.last_read_message_id !== null && cur.last_read_message_id >= messageId
              ? cur
              : { ...cur, last_read_message_id: messageId, unread_count: 0 },
          ),
        () => {
          // Offline or a server error: try again with the next message or visit.
          sent.current = null;
        },
      );
    },
    [api, queryClient, id, mark],
  );
}
