import type { Session, Task, TaskProject } from "@tolkovo/api-client";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { fakeApi, json, ownerSession, problem, type FakeRoutes } from "../../test/fake-api";
import { renderApp } from "../../test/render-app";

/** Moving tasks to another project and changing a project's key (ADR-0022, P4). */

const admin: Session = { ...ownerSession, permissions: [...ownerSession.permissions, "tasks.edit", "tasks.manage", "tasks.view"] };

const project = (id: string, patch: Partial<TaskProject>): TaskProject => ({
  id,
  name: id,
  key: id.toUpperCase(),
  former_keys: [],
  icon: null,
  color: "blue",
  access: "members",
  is_default: false,
  position: 1,
  member_ids: [],
  open_tasks: 0,
  archived_at: null,
  member: true,
  ...patch,
});

const projects: TaskProject[] = [
  project("p1", { name: "Задачи", key: "TSK", access: "team", is_default: true, position: 0 }),
  project("p2", { name: "Приложение", key: "APP", member_ids: ["u-anna"], former_keys: ["MOB"] }),
];

const task = (id: string, patch: Partial<Task>): Task => ({
  id,
  project_id: "p1",
  project_key: "TSK",
  number: 46,
  ref: "TSK-46",
  queue_id: null,
  topic_id: null,
  title: "Оплата картой",
  body: "",
  status: "open",
  priority: "normal",
  assignee_id: "u-petr",
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

const t1 = task("t1", {});
const t2 = task("t2", { number: 47, ref: "TSK-47", title: "Экран входа", assignee_id: "u-anna", version: 5 });

function server(routes: FakeRoutes = {}) {
  return fakeApi({
    session: admin,
    routes: {
      "GET /task-projects": () => json(200, { items: projects }),
      "GET /tasks": () => json(200, { items: [t1, t2], next_cursor: null }),
      "GET /tasks/summary": () => json(200, { assigned_open: 0, projects: [] }),
      "GET /tasks/t1": () => json(200, t1),
      "GET /tasks/t1/links": () => json(200, { items: [] }),
      "GET /tasks/t1/activity": () =>
        json(200, { items: [{ id: "a1", actor_id: admin.user.id, kind: "moved", from: "CRM-46", to: "TSK-46", created_at: "2026-11-10T11:00:00Z" }] }),
      "GET /task-queues": () =>
        json(200, {
          items: [
            { id: "q1", project_id: "p1", name: "Поддержка", member_ids: [], open_tasks: 0 },
            { id: "q2", project_id: "p2", name: "Мобильная", member_ids: [], open_tasks: 0 },
          ],
        }),
      "GET /task-tags": () => json(200, { items: [] }),
      "GET /task-settings/telegram-bot": () => json(200, { bot: null, encryption_available: true }),
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

describe("moving tasks between projects", () => {
  it("moves the open task with its queue; an assignee outside the project is added on request", async () => {
    const user = userEvent.setup();
    let answer = () => problem(409, "task_conflict");
    const api = server({ "POST /tasks/t1/move": () => answer() });
    renderApp("/tasks/t1", { api });
    expect(await screen.findByText(/Перенесена: CRM-46 → TSK-46/, {}, firstLook)).toBeInTheDocument();

    await user.click(await screen.findByRole("button", { name: "Перенести…" }));
    const dialog = screen.getByRole("dialog", { name: "Перенести TSK-46" });
    // Only the other project; its queues only.
    expect(within(dialog).getAllByRole("option", { name: /APP/ })).toHaveLength(1);
    expect(within(dialog).queryByRole("option", { name: /TSK/ })).not.toBeInTheDocument();
    expect(within(dialog).queryByRole("option", { name: "Поддержка" })).not.toBeInTheDocument();
    await user.selectOptions(within(dialog).getByLabelText("Очередь"), "Мобильная");
    expect(dialog).toHaveTextContent("Пётр Иванов не участник проекта «Приложение».");
    await user.click(within(dialog).getByRole("radio", { name: "Добавить в проект" }));

    await user.click(within(dialog).getByRole("button", { name: "Перенести" }));
    expect(await within(dialog).findByText(/Не удалось перенести 1 задачу/)).toBeInTheDocument();
    expect(api.calls.find((c) => c.path === "/tasks/t1/move")?.body).toEqual({
      project_id: "p2",
      queue_id: "q2",
      assignee_id: "u-petr",
      add_assignee_to_project: true,
      version: 3,
    });

    answer = () => json(200, { ...t1, project_id: "p2", project_key: "APP", number: 1, ref: "APP-1", queue_id: "q2", version: 4 });
    await user.click(within(dialog).getByRole("button", { name: "Перенести" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });

  it("moves the tasks chosen in the list; an assignee outside the project is unassigned by default", async () => {
    const user = userEvent.setup();
    const api = server({
      "POST /tasks/t1/move": () => json(200, { ...t1, project_id: "p2", ref: "APP-1" }),
      "POST /tasks/t2/move": () => json(200, { ...t2, project_id: "p2", ref: "APP-2" }),
    });
    renderApp("/tasks", { api });
    await user.click(await screen.findByRole("button", { name: "Выбрать" }, firstLook));
    await user.click(screen.getByRole("checkbox", { name: "Выбрать TSK-46" }));
    await user.click(screen.getByRole("checkbox", { name: "Выбрать TSK-47" }));
    expect(screen.getByText("Выбрано: 2")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Перенести…" }));
    const dialog = screen.getByRole("dialog", { name: "Перенести 2 задачи" });
    expect(dialog).toHaveTextContent("Пётр Иванов не участник проекта «Приложение».");
    await user.click(within(dialog).getByRole("button", { name: "Перенести" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    const bodies = api.calls.filter((c) => c.path.endsWith("/move")).map((c) => c.body);
    expect(bodies).toEqual([
      { project_id: "p2", queue_id: null, assignee_id: null, add_assignee_to_project: false, version: 3 },
      { project_id: "p2", queue_id: null, assignee_id: "u-anna", add_assignee_to_project: false, version: 5 },
    ]);
    // Done: the list leaves the choosing.
    expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();
  });

  it("changes a project's key and shows its former keys", async () => {
    const user = userEvent.setup();
    const api = server({ "PUT /task-projects/p2": () => problem(409, "project_exists") });
    renderApp("/settings/tasks", { api });
    const section = await screen.findByRole("region", { name: "Проекты" }, firstLook);
    const item = (await within(section).findAllByRole("listitem")).find((li) => li.textContent?.includes("Приложение"))!;
    await user.click(within(item).getByRole("button", { name: "Изменить" }));
    expect(within(section).getByText("MOB")).toBeInTheDocument();
    const key = within(section).getByLabelText("Ключ");
    await user.clear(key);
    await user.type(key, "crm");
    await user.click(within(section).getByRole("button", { name: /Сохранить/ }));
    expect(await within(section).findByText(/или этот ключ был у другого проекта/)).toBeInTheDocument();
    expect(api.calls.find((c) => c.method === "PUT")?.body).toMatchObject({ name: "Приложение", key: "CRM" });
  });
});
