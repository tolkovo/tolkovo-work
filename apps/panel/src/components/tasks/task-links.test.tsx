import type { Session, Task, TaskLink } from "@tolkovo/api-client";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { conv } from "../../test/inbox-fixtures";
import { fakeApi, json, ownerSession, problem, type FakeRoutes } from "../../test/fake-api";
import { renderApp } from "../../test/render-app";

/** Links between tasks (ADR-0022, P3): the «ждёт» plate, the section «Связи», duplicates. */

const admin: Session = { ...ownerSession, permissions: [...ownerSession.permissions, "tasks.edit", "tasks.manage", "tasks.view"] };

const task = (id: string, patch: Partial<Task>): Task => ({
  id,
  project_id: "p1",
  project_key: "TSK",
  number: 2,
  ref: "TSK-2",
  queue_id: null,
  topic_id: null,
  title: "Экран оплаты",
  body: "",
  status: "open",
  priority: "normal",
  assignee_id: null,
  created_by: admin.user.id,
  created_at: "2026-11-10T10:00:00Z",
  updated_at: "2026-11-10T10:00:00Z",
  closed_at: null,
  status_changed_at: "2026-11-10T10:00:00Z",
  blocked_reason: null,
  waiting_on: [],
  version: 3,
  conversation_ids: [],
  conversation_count: 0,
  tag_ids: [],
  files: [],
  ...patch,
});

const waiting = task("t2", { waiting_on: [{ id: "t1", ref: "TSK-1" }, { id: "s1", ref: null }], conversation_ids: ["c1"] });

const links: TaskLink[] = [
  { kind: "waits_for", task_id: "t1", visible: true, ref: "TSK-1", title: "API оплаты", status: "in_progress", created_at: "2026-11-10T11:00:00Z" },
  { kind: "relates", task_id: "s1", visible: false, ref: null, title: null, status: "open", created_at: "2026-11-10T12:00:00Z" },
];

function server(routes: FakeRoutes = {}) {
  return fakeApi({
    session: admin,
    routes: {
      "GET /tasks": () => json(200, { items: [waiting, task("t3", { number: 3, ref: "TSK-3", title: "Релиз" })], next_cursor: null }),
      "GET /tasks/summary": () => json(200, { assigned_open: 0, projects: [] }),
      "GET /tasks/t2": () => json(200, waiting),
      "GET /tasks/t2/links": () => json(200, { items: links }),
      "GET /tasks/t2/activity": () =>
        json(200, {
          items: [
            { id: "a1", actor_id: admin.user.id, kind: "link_added", link: "waits_for", link_task_id: "t1", link_ref: "TSK-1", created_at: "2026-11-10T11:00:00Z" },
            { id: "a2", actor_id: admin.user.id, kind: "link_added", link: "relates", link_task_id: "s1", created_at: "2026-11-10T12:00:00Z" },
          ],
        }),
      "GET /conversations/c1": () => json(200, conv("c1")),
      "GET /task-queues": () => json(200, { items: [] }),
      "GET /task-board-settings": () => json(200, { rules: [] }),
      "GET /task-settings": () =>
        json(200, {
          settings: { enabled: true, conversation_button: "both", reply_ru: "Готово", reply_en: "Done" },
          defaults: { reply_ru: "Готово", reply_en: "Done" },
          placeholders: ["name"],
        }),
      ...routes,
    },
  });
}

const firstLook = { timeout: 3000 };

describe("links between tasks", () => {
  it("shows what the task waits for, its links and the lines of its feed; a hidden task by its status only", async () => {
    renderApp("/tasks/t2", { api: server() });
    const article = await screen.findByRole("article", {}, firstLook);
    // The plate names the first task it waits for and counts the rest.
    expect(within(article).getByTitle("Ждёт: TSK-1, скрытая задача")).toHaveTextContent("ждёт TSK-1+1");

    const section = await screen.findByRole("region", { name: "Связи" });
    const rows = await within(section).findAllByRole("listitem");
    expect(rows[0]).toHaveTextContent("Ждёт");
    expect(within(rows[0]!).getByRole("link", { name: /TSK-1\s*API оплаты/ })).toHaveAttribute("href", expect.stringContaining("/tasks/t1"));
    expect(rows[1]).toHaveTextContent("Задача в проекте, где вы не участник");
    expect(within(rows[1]!).queryByRole("link")).not.toBeInTheDocument();

    expect(await screen.findByText(/Связь: Ждёт TSK-1/)).toBeInTheDocument();
    expect(screen.getByText(/Связь: Связана с скрытая задача/)).toBeInTheDocument();
  });

  it("links by the other task's ref, tells a loop, and removes a link", async () => {
    const user = userEvent.setup();
    let answer = () => problem(409, "link_loop");
    const api = server({
      "POST /tasks/t2/links": () => answer(),
      "DELETE /tasks/t2/links/t1": () => new Response(null, { status: 204 }),
    });
    renderApp("/tasks/t2", { api });
    const section = await screen.findByRole("region", { name: "Связи" }, firstLook);
    await user.click(within(section).getByRole("button", { name: "Добавить связь" }));
    await user.selectOptions(within(section).getByLabelText("Связь"), "Блокирует");
    await user.type(within(section).getByLabelText("Задача"), "CRM-41");
    await user.click(within(section).getByRole("button", { name: "Связать" }));
    expect(await within(section).findByText("Задачи ждали бы друг друга по кругу. Уберите одну из связей.")).toBeInTheDocument();
    expect(api.calls.find((c) => c.method === "POST")?.body).toEqual({ kind: "blocks", task: "CRM-41" });

    answer = () => json(201, { items: links });
    await user.click(within(section).getByRole("button", { name: "Связать" }));
    await waitFor(() => expect(within(section).queryByRole("button", { name: "Связать" })).not.toBeInTheDocument());

    await user.click(within(section).getByRole("button", { name: "Убрать связь с TSK-1 API оплаты" }));
    await waitFor(() => expect(api.calls.some((c) => c.method === "DELETE" && c.path === "/tasks/t2/links/t1")).toBe(true));
  });

  it("closes a task as a duplicate after saying its conversations move to the original", async () => {
    const user = userEvent.setup();
    const api = server({ "POST /tasks/t2/duplicate": () => json(200, { ...waiting, status: "canceled", conversation_ids: [] }) });
    renderApp("/tasks/t2", { api });
    await user.click(await screen.findByRole("button", { name: "Закрыть как дубликат…" }, firstLook));
    const dialog = screen.getByRole("dialog", { name: "Закрыть TSK-2 как дубликат" });
    expect(dialog).toHaveTextContent("Её 1 диалог перейдёт к оригиналу и будут ждать его.");
    await user.type(within(dialog).getByLabelText("Номер оригинала"), "TSK-1");
    await user.click(within(dialog).getByRole("button", { name: "Закрыть как дубликат" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(api.calls.find((c) => c.path === "/tasks/t2/duplicate")?.body).toEqual({ original: "TSK-1", version: 3 });
  });

  it("puts the plate in the list", async () => {
    renderApp("/tasks", { api: server() });
    const row = await screen.findByRole("link", { name: /Экран оплаты/ }, firstLook);
    expect(row).toHaveTextContent("ждёт TSK-1");
    expect(screen.getByRole("link", { name: /Релиз/ })).not.toHaveTextContent("ждёт");
  });
});
