import type { Conversation, ConversationPage, MessagePage } from "@tolkovo/api-client";
import { queryKeys } from "@tolkovo/api-client/react";
import { QueryClient, type InfiniteData } from "@tanstack/react-query";
import { describe, expect, it } from "vitest";

import { msg } from "../../test/inbox-fixtures";
import {
  addToFeed,
  applyConversation,
  dropConversation,
  matchesFilter,
  patchConversation,
  queriesShowingContact,
  upsertIntoPages,
} from "./cache";
import { tabFilter } from "./tabs";

function conv(id: string, minute: number, patch: Partial<Conversation> = {}): Conversation {
  return {
    id,
    number: 1,
    status: "queued",
    priority: 0,
    line_level: 1,
    contact: { id: `k-${id}`, name: `Contact ${id}` },
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
    last_read_message_id: null,
    unread_count: 0,
    rating: null,
    ...patch,
  };
}

type Pages = InfiniteData<ConversationPage, string | undefined>;
const pages = (...ps: [Conversation[], string | null][]): Pages => ({
  pages: ps.map(([items, next_cursor]) => ({ items, next_cursor })),
  pageParams: ps.map((_, i) => (i === 0 ? undefined : `c${i}`)),
});
const ids = (data: Pages | undefined) => data?.pages.map((p) => p.items.map((c) => c.id));

describe("matchesFilter", () => {
  it("follows the tabs as the server filters them", () => {
    const mine = conv("a", 1, { status: "assigned", assignee_id: "me" });
    const theirs = conv("b", 1, { status: "assigned", assignee_id: "u2" });
    const queued = conv("c", 1);
    expect(matchesFilter(tabFilter("mine"), mine, "me")).toBe(true);
    expect(matchesFilter(tabFilter("mine"), theirs, "me")).toBe(false);
    expect(matchesFilter(tabFilter("mine"), mine, undefined)).toBe(false);
    expect(matchesFilter(tabFilter("queue"), queued, "me")).toBe(true);
    expect(matchesFilter(tabFilter("queue"), mine, "me")).toBe(false);
    expect(matchesFilter(tabFilter("all"), conv("d", 1, { status: "closed" }), "me")).toBe(true);
  });

  it("keeps a contact's history to that contact, whatever the status", () => {
    expect(matchesFilter({ contact_id: "k-a" }, conv("a", 1, { status: "closed" }), "me")).toBe(true);
    expect(matchesFilter({ contact_id: "k-a" }, conv("b", 1), "me")).toBe(false);
  });
});

describe("upsertIntoPages", () => {
  it("moves a conversation with new activity to the top", () => {
    const before = pages([[conv("a", 9), conv("b", 8)], "c1"], [[conv("c", 7)], null]);
    expect(ids(upsertIntoPages(before, conv("c", 10)))).toEqual([["c", "a", "b"], []]);
  });

  it("keeps the server order inside pages, ties broken by id", () => {
    const before = pages([[conv("b", 9), conv("a", 5)], null]);
    expect(ids(upsertIntoPages(before, conv("c", 9)))).toEqual([["c", "b", "a"]]);
    expect(ids(upsertIntoPages(before, conv("x", 7)))).toEqual([["b", "x", "a"]]);
  });

  it("leaves out a row older than everything loaded while more pages exist", () => {
    const before = pages([[conv("a", 9)], "c1"]);
    expect(ids(upsertIntoPages(before, conv("z", 1)))).toEqual([["a"]]);
    // On the last page it belongs at the end, also into an empty list.
    expect(ids(upsertIntoPages(pages([[conv("a", 9)], null]), conv("z", 1)))).toEqual([["a", "z"]]);
    expect(ids(upsertIntoPages(pages([[], null]), conv("z", 1)))).toEqual([["z"]]);
  });
});

describe("applyConversation", () => {
  it("patches every cached list and the detail query", () => {
    const qc = new QueryClient();
    const queue = queryKeys.conversationList(tabFilter("queue"));
    const mine = queryKeys.conversationList(tabFilter("mine"));
    qc.setQueryData(queue, pages([[conv("a", 5), conv("b", 4)], null]));
    qc.setQueryData(mine, pages([[conv("m", 3, { status: "assigned", assignee_id: "me" })], null]));

    // "a" is taken by me: it leaves the queue and joins «Мои» on top.
    const taken = conv("a", 6, { status: "assigned", assignee_id: "me", version: 2 });
    applyConversation(qc, taken, "me");
    expect(ids(qc.getQueryData<Pages>(queue))).toEqual([["b"]]);
    expect(ids(qc.getQueryData<Pages>(mine))).toEqual([["a", "m"]]);
    expect(qc.getQueryData(queryKeys.conversation("a"))).toEqual(taken);

    dropConversation(qc, "a");
    expect(ids(qc.getQueryData<Pages>(mine))).toEqual([["m"]]);
    expect(qc.getQueryData(queryKeys.conversation("a"))).toBeUndefined();
  });

  it("finds the queries that show a contact", () => {
    const qc = new QueryClient();
    const queue = queryKeys.conversationList(tabFilter("queue"));
    qc.setQueryData(queue, pages([[conv("a", 5)], null]));
    qc.setQueryData(queryKeys.conversation("b"), conv("b", 1));
    expect(queriesShowingContact(qc, "k-a")).toEqual([queue]);
    expect(queriesShowingContact(qc, "k-b")).toEqual([queryKeys.conversation("b")]);
    expect(queriesShowingContact(qc, "k-none")).toEqual([]);
  });
});

describe("patchConversation", () => {
  it("changes the conversation in place in every list and the detail query", () => {
    const queryClient = new QueryClient();
    const all = queryKeys.conversationList(tabFilter("all"));
    queryClient.setQueryData(all, pages([[conv("a", 9), conv("b", 8, { unread_count: 3 })], null]));
    queryClient.setQueryData(queryKeys.conversation("b"), conv("b", 8, { unread_count: 3 }));
    patchConversation(queryClient, "b", (c) => ({ ...c, unread_count: 0 }));
    const list = queryClient.getQueryData<Pages>(all);
    expect(ids(list)).toEqual([["a", "b"]]);
    expect(list?.pages[0]?.items[1]?.unread_count).toBe(0);
    expect(queryClient.getQueryData<Conversation>(queryKeys.conversation("b"))?.unread_count).toBe(0);
  });
});

describe("addToFeed", () => {
  it("puts a sent message on top of the newest page once", () => {
    const queryClient = new QueryClient();
    const key = queryKeys.conversationMessages("c1");
    queryClient.setQueryData<InfiniteData<MessagePage, string | undefined>>(key, {
      pages: [{ items: [msg("m1", {})], next_cursor: null }],
      pageParams: [undefined],
    });
    addToFeed(queryClient, msg("m2", {}));
    addToFeed(queryClient, msg("m2", {}));
    const feed = queryClient.getQueryData<InfiniteData<MessagePage, string | undefined>>(key);
    expect(feed?.pages[0]?.items.map((m) => m.id)).toEqual(["m2", "m1"]);
  });
});
