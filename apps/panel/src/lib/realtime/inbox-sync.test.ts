import type { Conversation, ConversationPage, Session } from "@tolkovo/api-client";
import { queryKeys } from "@tolkovo/api-client/react";
import { QueryClient, type InfiniteData } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { fakeApi, json, ownerSession, problem, type FakeRoutes } from "../../test/fake-api";
import { createPanelApiClient } from "../api";
import { tabFilter } from "../inbox/tabs";
import { createInboxSync } from "./inbox-sync";
import { applySignal, useSignalStore } from "./signals";

const conv = (id: string, minute: number, patch: Partial<Conversation> = {}): Conversation => ({
  id,
  number: 1,
  status: "queued",
  priority: 0,
  line_level: 1,
  contact: { id: `k-${id}`, name: id },
  assignee_id: null,
  channel_id: null,
  subject: null,
  first_response_at: null,
  last_message_at: `2026-10-04T12:${String(minute).padStart(2, "0")}:00Z`,
  resolved_at: null,
  closed_at: null,
  awaiting_fix: false,
  version: 1,
  created_at: "2026-10-04T12:00:00Z",
  updated_at: "2026-10-04T12:00:00Z",
  last_message: null,
  tag_ids: [],
  topic_id: null,
  last_read_message_id: null,
  unread_count: 0,
  rating: null,
  ...patch,
});

type Pages = InfiniteData<ConversationPage, string | undefined>;
const one = (items: Conversation[]): Pages => ({ pages: [{ items, next_cursor: null }], pageParams: [undefined] });
const ids = (data: Pages | undefined) => data?.pages.flatMap((p) => p.items.map((c) => c.id));
const event = (type: string, data: Record<string, unknown>) => ({ type, id: "e", ts: "2026-10-04T12:00:00Z", seq: 1, data });

function setup(routes: FakeRoutes) {
  const api = fakeApi({ routes });
  const queryClient = new QueryClient();
  queryClient.setQueryData<Session>(queryKeys.session, ownerSession);
  const client = createPanelApiClient(queryClient, { origin: "http://panel.test", fetch: api.fetch });
  const sync = createInboxSync({ queryClient, api: client });
  return { api, queryClient, sync };
}

describe("createInboxSync", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it("fetches a changed conversation once per burst and moves it between lists", async () => {
    const me = ownerSession.user.id;
    const { api, queryClient, sync } = setup({
      "GET /conversations/c1": () => json(200, conv("c1", 9, { status: "assigned", assignee_id: me, version: 3 })),
    });
    const queue = queryKeys.conversationList(tabFilter("queue"));
    const mine = queryKeys.conversationList(tabFilter("mine"));
    queryClient.setQueryData(queue, one([conv("c1", 5), conv("c2", 4)]));
    queryClient.setQueryData(mine, one([]));
    const invalidate = vi.spyOn(queryClient, "invalidateQueries");

    sync.onEvent(event("conversation.assigned", { conversation_id: "c1" }));
    sync.onEvent(event("conversation.status_changed", { conversation_id: "c1" }));
    await vi.advanceTimersByTimeAsync(50);

    expect(api.calls.filter((c) => c.path === "/conversations/c1")).toHaveLength(1);
    expect(ids(queryClient.getQueryData<Pages>(queue))).toEqual(["c2"]);
    expect(ids(queryClient.getQueryData<Pages>(mine))).toEqual(["c1"]);
    // Counters are cheap and exact on the server: refetched, not computed here.
    expect(invalidate.mock.calls.map(([f]) => f?.queryKey)).toEqual([queryKeys.inboxCounts]);
    sync.stop();
  });

  it("refetches the feed of a new message and ends the sender's typing", async () => {
    const { queryClient, sync } = setup({ "GET /conversations/c1": () => json(200, conv("c1", 9)) });
    applySignal({
      type: "conversation.typing",
      id: "t",
      ts: "2026-10-04T12:00:00Z",
      data: { conversation_id: "c1", actor: "contact", actor_id: "k-c1", state: "start" },
    });
    const invalidate = vi.spyOn(queryClient, "invalidateQueries");
    sync.onEvent(
      event("message.created", { conversation_id: "c1", message_id: "m", sender_type: "contact", sender_id: "k-c1" }),
    );
    expect(useSignalStore.getState().typing.c1).toEqual([]);
    await vi.advanceTimersByTimeAsync(50);
    expect(invalidate.mock.calls.map(([f]) => f?.queryKey)).toContainEqual(queryKeys.conversationMessages("c1"));
    sync.stop();
  });

  it("drops a conversation that is gone and never applies an older version over a newer one", async () => {
    let answer: Response = problem(404, "not_found");
    const { queryClient, sync } = setup({ "GET /conversations/c1": () => answer });
    const all = queryKeys.conversationList(tabFilter("all"));
    queryClient.setQueryData(all, one([conv("c1", 5)]));
    sync.onEvent(event("conversation.status_changed", { conversation_id: "c1" }));
    await vi.advanceTimersByTimeAsync(50);
    expect(ids(queryClient.getQueryData<Pages>(all))).toEqual([]);

    answer = json(200, conv("c1", 9, { version: 5, subject: "new" }));
    sync.onEvent(event("conversation.status_changed", { conversation_id: "c1" }));
    await vi.advanceTimersByTimeAsync(50);
    answer = json(200, conv("c1", 8, { version: 4, subject: "old" }));
    sync.onEvent(event("conversation.status_changed", { conversation_id: "c1" }));
    await vi.advanceTimersByTimeAsync(50);
    expect(queryClient.getQueryData<Conversation>(queryKeys.conversation("c1"))?.subject).toBe("new");
    sync.stop();
  });

  it("refreshes unread counters only for the reader's own read marks", async () => {
    const { api, queryClient, sync } = setup({ "GET /conversations/c1": () => json(200, conv("c1", 5)) });
    const invalidate = vi.spyOn(queryClient, "invalidateQueries");
    sync.onEvent(event("conversation.read", { conversation_id: "c1", reader_type: "operator", reader_id: "u-someone" }));
    await vi.advanceTimersByTimeAsync(50);
    expect(api.calls.filter((c) => c.path === "/conversations/c1")).toHaveLength(0);

    sync.onEvent(event("conversation.read", { conversation_id: "c1", reader_type: "operator", reader_id: ownerSession.user.id }));
    sync.onEvent(event("conversation.tags_changed", { conversation_id: "c1" }));
    await vi.advanceTimersByTimeAsync(50);
    expect(api.calls.filter((c) => c.path === "/conversations/c1")).toHaveLength(1);
    // The own read mark changes mine_unread (the tab title), the tags change no counter.
    expect(invalidate.mock.calls.map(([f]) => f?.queryKey)).toEqual([queryKeys.inboxCounts]);
    sync.stop();
  });

  it("refetches a conversation the customer rated, counters stay", async () => {
    const { api, queryClient, sync } = setup({ "GET /conversations/c1": () => json(200, conv("c1", 5)) });
    const invalidate = vi.spyOn(queryClient, "invalidateQueries");
    sync.onEvent(event("conversation.rated", { conversation_id: "c1" }));
    await vi.advanceTimersByTimeAsync(50);
    expect(api.calls.filter((c) => c.path === "/conversations/c1")).toHaveLength(1);
    expect(invalidate).not.toHaveBeenCalled();
    sync.stop();
  });

  it("refetches the issues of a conversation and the conversation when they change", async () => {
    const { api, queryClient, sync } = setup({ "GET /conversations/c1": () => json(200, conv("c1", 5)) });
    const invalidate = vi.spyOn(queryClient, "invalidateQueries");
    sync.onEvent(event("conversation.issues_changed", { conversation_id: "c1" }));
    await vi.advanceTimersByTimeAsync(50);
    // awaiting_fix lives on the conversation; no counter changes.
    expect(api.calls.filter((c) => c.path === "/conversations/c1")).toHaveLength(1);
    expect(invalidate.mock.calls.map(([f]) => f?.queryKey)).toEqual([
      queryKeys.conversationIssues("c1"),
      queryKeys.openGitHubIssues,
    ]);
    sync.stop();
  });

  it("refetches the tasks of a conversation and the section when tasks change", async () => {
    const { queryClient, sync } = setup({ "GET /conversations/c1": () => json(200, conv("c1", 5)) });
    const invalidate = vi.spyOn(queryClient, "invalidateQueries");
    sync.onEvent(event("conversation.tasks_changed", { conversation_id: "c1" }));
    sync.onEvent(event("task.changed", { task_id: "t1", assignee_id: null, actor_id: "u2", assigned: false }));
    await vi.advanceTimersByTimeAsync(50);
    expect(invalidate.mock.calls.map(([f]) => f?.queryKey)).toEqual([queryKeys.conversationTasks("c1"), queryKeys.tasks]);
    sync.stop();
  });

  it("tells the member about a task someone else gave them, not about their own", async () => {
    vi.setSystemTime(new Date("2026-10-04T12:00:30Z"));
    const me = ownerSession.user.id;
    const task = {
      id: "t1", number: 7, queue_id: null, topic_id: null, title: "Сертификат", body: "", status: "open", priority: "high",
      assignee_id: me, created_by: "u2", created_at: "2026-10-04T12:00:00Z", updated_at: "2026-10-04T12:00:00Z",
      closed_at: null, version: 2, conversation_ids: [], files: [], tag_ids: [],
    };
    const api = fakeApi({ routes: { "GET /tasks/t1": () => json(200, task) } });
    const queryClient = new QueryClient();
    queryClient.setQueryData<Session>(queryKeys.session, ownerSession);
    const client = createPanelApiClient(queryClient, { origin: "http://panel.test", fetch: api.fetch });
    const onTaskAssigned = vi.fn();
    const sync = createInboxSync({ queryClient, api: client, onTaskAssigned });

    sync.onEvent(event("task.changed", { task_id: "t1", assignee_id: me, actor_id: me, assigned: true }));
    sync.onEvent(event("task.changed", { task_id: "t1", assignee_id: me, actor_id: "u2", assigned: false }));
    await vi.advanceTimersByTimeAsync(50);
    expect(onTaskAssigned).not.toHaveBeenCalled();

    sync.onEvent(event("task.changed", { task_id: "t1", assignee_id: me, actor_id: "u2", assigned: true }));
    await vi.advanceTimersByTimeAsync(50);
    expect(onTaskAssigned).toHaveBeenCalledWith(expect.objectContaining({ id: "t1", number: 7 }));
    sync.stop();
  });

  it("hands each fetched conversation with its events to the notifications", async () => {
    const api = fakeApi({ routes: { "GET /conversations/c1": () => json(200, conv("c1", 9)) } });
    const queryClient = new QueryClient();
    queryClient.setQueryData<Session>(queryKeys.session, ownerSession);
    const client = createPanelApiClient(queryClient, { origin: "http://panel.test", fetch: api.fetch });
    const onConversation = vi.fn();
    const sync = createInboxSync({ queryClient, api: client, onConversation });
    const invalidate = vi.spyOn(queryClient, "invalidateQueries");

    const created = event("conversation.created", { conversation_id: "c1", status: "queued" });
    const message = event("message.created", { conversation_id: "c1", sender_type: "contact" });
    sync.onEvent(created);
    sync.onEvent(message);
    sync.onEvent(event("conversation.read", { conversation_id: "c1", reader_id: ownerSession.user.id }));
    await vi.advanceTimersByTimeAsync(50);

    expect(onConversation).toHaveBeenCalledTimes(1);
    expect(onConversation).toHaveBeenCalledWith([created, message], expect.objectContaining({ id: "c1" }), ownerSession.user.id);
    // A customer message changes mine_unread.
    expect(invalidate.mock.calls.map(([f]) => f?.queryKey)).toContainEqual(queryKeys.inboxCounts);

    // A later batch with only a read mark says nothing new.
    sync.onEvent(event("conversation.read", { conversation_id: "c1", reader_id: ownerSession.user.id }));
    await vi.advanceTimersByTimeAsync(50);
    expect(onConversation).toHaveBeenCalledTimes(1);
    sync.stop();
  });

  it("refetches only the queries that show a changed contact", async () => {
    const { queryClient, sync } = setup({});
    const queue = queryKeys.conversationList(tabFilter("queue"));
    const mine = queryKeys.conversationList(tabFilter("mine"));
    queryClient.setQueryData(queue, one([conv("c1", 5)]));
    queryClient.setQueryData(mine, one([conv("c2", 5)]));
    const invalidate = vi.spyOn(queryClient, "invalidateQueries").mockResolvedValue();
    sync.onEvent(event("contact.updated", { contact_id: "k-c1" }));
    await vi.advanceTimersByTimeAsync(50);
    expect(invalidate.mock.calls.map(([f]) => f?.queryKey)).toEqual([queue, queryKeys.contacts]);
    sync.stop();
  });
});
