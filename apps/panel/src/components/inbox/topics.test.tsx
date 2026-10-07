import type { Conversation, Topic } from "@tolkovo/api-client";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { fakeApi, json } from "../../test/fake-api";
import { conv, inboxOperator, msg } from "../../test/inbox-fixtures";
import { renderApp } from "../../test/render-app";

const CRM_ID = "0199a000-0000-7000-8000-00000000c0a1";
const OF_ID = "0199a000-0000-7000-8000-00000000c0a2";

const topics: Topic[] = [
  { id: CRM_ID, name: "CRM", color: "blue", position: 0, priority: 2, task_queue_id: null, member_ids: [] },
  { id: OF_ID, name: "OnlyFans", color: "violet", position: 1, priority: 0, task_queue_id: null, member_ids: [] },
];

describe("topics in the inbox", () => {
  it("shows the topic on rows and filters the list by it", async () => {
    const user = userEvent.setup();
    const crm = conv("c1", { topic_id: CRM_ID });
    const loose = conv("c2", { topic_id: null, contact: { id: "k-c2", name: "Олег Петров" } });
    const api = fakeApi({
      session: inboxOperator,
      routes: {
        "GET /topics": () => json(200, { items: topics }),
        "GET /conversations": (r) => {
          const topic = new URL(r.url).searchParams.get("topic");
          const items = topic === null ? [crm, loose] : topic === "none" ? [loose] : [crm, loose].filter((c) => c.topic_id === topic);
          return json(200, { items, next_cursor: null });
        },
      },
    });
    renderApp("/inbox?tab=all", { api });
    const row = (await screen.findByText("Марина Ковалёва")).closest("a")!;
    expect(within(row).getByText("CRM")).toBeInTheDocument();

    const filter = screen.getByRole("group", { name: "Темы вопросов" });
    expect(within(filter).getByRole("button", { name: "Все темы" })).toHaveAttribute("aria-pressed", "true");
    await user.click(within(filter).getByRole("button", { name: "CRM" }));
    await waitFor(() => expect(screen.queryByText("Олег Петров")).not.toBeInTheDocument());
    expect(within(filter).getByRole("button", { name: "CRM" })).toHaveAttribute("aria-pressed", "true");
    expect(within(filter).getByRole("button", { name: "Все темы" })).toHaveAttribute("aria-pressed", "false");
    expect(api.calls.some((c) => c.path === "/conversations" && c.method === "GET")).toBe(true);
    // The tabs keep the topic.
    expect(screen.getByRole("link", { name: /Мои/ })).toHaveAttribute("href", expect.stringContaining(`topic=${CRM_ID}`));

    await user.click(within(filter).getByRole("button", { name: "Без темы" }));
    expect(await screen.findByText("Олег Петров")).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByText("Марина Ковалёва")).not.toBeInTheDocument());
  });

  it("changes the topic in the conversation and names it in the feed", async () => {
    const user = userEvent.setup();
    let conversation: Conversation = conv("c1");
    const api = fakeApi({
      session: inboxOperator,
      routes: {
        "GET /topics": () => json(200, { items: topics }),
        "GET /conversations": () => json(200, { items: [conversation], next_cursor: null }),
        "GET /conversations/c1": () => json(200, conversation),
        "GET /conversations/c1/messages": () =>
          json(200, {
            items: [
              msg("m2", {
                kind: "event",
                sender_type: "system",
                sender_id: null,
                event: {
                  type: "topic_changed",
                  from: "assigned",
                  to: "assigned",
                  assignee_id: null,
                  previous_assignee_id: null,
                  topic_id: OF_ID,
                  previous_topic_id: null,
                  by_customer: true,
                },
              }),
              msg("m1", { body: "Не грузится список моделей" }),
            ],
            next_cursor: null,
          }),
        "PUT /conversations/c1/topic": async (r) => {
          const body = (await r.json()) as { topic_id: string | null };
          conversation = { ...conversation, topic_id: body.topic_id, priority: 2, version: conversation.version + 1 };
          return json(200, conversation);
        },
        "GET /contacts/k-c1": () => json(404, {}),
      },
    });
    renderApp("/inbox/c1", { api });
    expect(await screen.findByText(/Клиент выбрал тему «OnlyFans»/)).toBeInTheDocument();
    const select = await screen.findByLabelText("Тема вопроса");
    expect(select).toHaveValue("");
    await user.selectOptions(select, CRM_ID);
    await waitFor(() => expect(api.calls.find((c) => c.method === "PUT")?.body).toEqual({ topic_id: CRM_ID, version: 1 }));
    await waitFor(() => expect(screen.getByLabelText("Тема вопроса")).toHaveValue(CRM_ID));
  });
});
