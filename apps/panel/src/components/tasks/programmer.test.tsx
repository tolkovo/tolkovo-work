import type { Task } from "@tolkovo/api-client";
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { fakeApi, json, programmerSession } from "../../test/fake-api";
import { conv, msg } from "../../test/inbox-fixtures";
import { renderApp } from "../../test/render-app";

const task: Task = {
  id: "t1",
  number: 12,
  queue_id: null,
  topic_id: null,
  title: "Двойное списание при оплате",
  body: "",
  status: "in_progress",
  priority: "high",
  assignee_id: programmerSession.user.id,
  created_by: "u-petr",
  created_at: "2026-10-06T10:00:00Z",
  updated_at: "2026-10-06T10:00:00Z",
  closed_at: null,
  status_changed_at: "2026-10-06T10:00:00Z",
  blocked_reason: null,
  version: 3,
  conversation_ids: ["c1"],
  conversation_count: 1,
  tag_ids: [],
  files: [],
};

function server() {
  return fakeApi({
    session: programmerSession,
    routes: {
      "GET /tasks": () => json(200, { items: [task], next_cursor: null }),
      "GET /tasks/summary": () => json(200, { assigned_open: 1 }),
      "GET /tasks/t1": () => json(200, task),
      "GET /tasks/t1/activity": () => json(200, { items: [] }),
      "GET /task-queues": () => json(200, { items: [] }),
      "GET /task-board-settings": () => json(200, { rules: [] }),
      "GET /conversations/c1": () => json(200, conv("c1", { assignee_id: "u-petr" })),
      "GET /conversations/c1/messages": () =>
        json(200, { items: [msg("m1", { body: "Списали дважды" })], next_cursor: null }),
      "GET /conversations/c1/tasks": () =>
        json(200, { items: [task], settings: { enabled: true, conversation_button: "both" }, reply_draft: null }),
      "GET /conversations": () => json(403, { type: "about:blank", title: "Forbidden", status: 403, code: "forbidden" }),
    },
  });
}

/** The first look at a page waits for its lazy route to load, slow under a full parallel run. */
const firstLook = { timeout: 3000 };

describe("the programmer (ADR-0020)", () => {
  it("has no inbox: the home screen is the tasks section", async () => {
    const api = server();
    renderApp("/", { api });
    expect(await screen.findByRole("link", { name: /Двойное списание при оплате/ }, firstLook)).toBeInTheDocument();
    const nav = screen.getByRole("navigation", { name: "Разделы" });
    expect(within(nav).queryByRole("link", { name: "Диалоги" })).not.toBeInTheDocument();
    expect(within(nav).getByRole("link", { name: /^Задачи/ })).toBeInTheDocument();
  });

  it("reads a linked conversation next to the task and leaves only notes there", async () => {
    const user = userEvent.setup();
    const api = server();
    renderApp("/tasks/t1", { api });

    const link = await screen.findByRole("link", { name: "№7 · Марина Ковалёва" }, firstLook);
    expect(link).toHaveAttribute("href", expect.stringMatching(/^\/tasks\/t1\/conversations\/c1/));
    await user.click(link);

    expect(await screen.findByText("Списали дважды", {}, firstLook)).toBeInTheDocument();
    // No customer card: the programmer does not see contacts.
    expect(screen.queryByRole("button", { name: "Карточка клиента" })).not.toBeInTheDocument();
    expect(api.calls.some((c) => c.path.startsWith("/contacts/"))).toBe(false);
    // The composer writes notes only.
    expect(screen.queryByRole("button", { name: "Ответ" })).not.toBeInTheDocument();

    await user.click(screen.getByRole("link", { name: "Назад к задаче" }));
    expect(await screen.findByRole("heading", { name: /#12.*Двойное списание при оплате/ })).toBeInTheDocument();
  });
});
