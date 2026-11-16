import type { Session, Task, TaskProject } from "@tolkovo/api-client";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { fakeApi, json, ownerSession, problem, type FakeRoutes } from "../../test/fake-api";
import { renderApp } from "../../test/render-app";

/** Projects of tasks (ADR-0022): a switch in the section, a project for a new task, settings. */

const admin: Session = { ...ownerSession, permissions: [...ownerSession.permissions, "tasks.edit", "tasks.manage", "tasks.view"] };

const project = (id: string, patch: Partial<TaskProject>): TaskProject => ({
  id,
  name: id,
  key: id.toUpperCase(),
  icon: null,
  color: "blue",
  access: "members",
  is_default: false,
  position: 1,
  member_ids: [],
  open_tasks: 0,
  archived_at: null,
  member: false,
  ...patch,
});

const projects: TaskProject[] = [
  project("p1", { name: "Задачи", key: "TSK", access: "team", is_default: true, position: 0, member: true }),
  project("p2", { name: "CRM", key: "CRM", member_ids: ["u-anna"], open_tasks: 3, member: true }),
  project("p3", { name: "Склад", key: "WH" }),
];

const task: Task = {
  id: "t1",
  project_id: "p2",
  project_key: "CRM",
  number: 41,
  ref: "CRM-41",
  queue_id: null,
  topic_id: null,
  title: "Импорт контактов",
  body: "",
  status: "open",
  priority: "normal",
  assignee_id: null,
  created_by: admin.user.id,
  created_at: "2026-10-06T10:00:00Z",
  updated_at: "2026-10-06T10:00:00Z",
  closed_at: null,
  status_changed_at: "2026-10-06T10:00:00Z",
  blocked_reason: null,
  version: 1,
  conversation_ids: [],
  conversation_count: 0,
  tag_ids: [],
  files: [],
};

function server(routes: FakeRoutes = {}) {
  return fakeApi({
    session: admin,
    routes: {
      "GET /task-projects": () => json(200, { items: projects }),
      "GET /tasks": () => json(200, { items: [task], next_cursor: null }),
      "GET /tasks/summary": () => json(200, { assigned_open: 0 }),
      "GET /tasks/t1": () => json(200, task),
      "GET /tasks/t1/activity": () => json(200, { items: [] }),
      "GET /task-queues": () =>
        json(200, {
          items: [
            { id: "q-dev", project_id: "p1", name: "Разработка", member_ids: [], open_tasks: 0 },
            { id: "q-crm", project_id: "p2", name: "Интеграции", member_ids: [], open_tasks: 0 },
          ],
        }),
      "GET /task-settings": () =>
        json(200, {
          settings: { enabled: true, conversation_button: "both", reply_ru: "Готово", reply_en: "Done" },
          defaults: { reply_ru: "Готово", reply_en: "Done" },
          placeholders: ["name"],
        }),
      "GET /task-settings/telegram-bot": () => json(200, { bot: null, encryption_available: true }),
      "GET /task-board-settings": () => json(200, { rules: [] }),
      ...routes,
    },
  });
}

const firstLook = { timeout: 3000 };

describe("projects of tasks", () => {
  it("switches the section to a project by its key and asks for its tasks only", async () => {
    const user = userEvent.setup();
    const api = server();
    const { router } = renderApp("/tasks?view=open", { api });
    // The task is named by its project's key.
    expect(await screen.findByRole("link", { name: /CRM-41.*Импорт контактов|Импорт контактов/ }, firstLook)).toHaveTextContent("CRM-41");

    await user.click(screen.getByRole("button", { name: "Проект: Все проекты" }));
    const menu = screen.getByRole("menu");
    // Projects without me are apart; admins also get a new project.
    expect(within(within(menu).getByRole("group", { name: "Без вас" })).getByRole("menuitemradio", { name: /Склад/ })).toBeInTheDocument();
    expect(within(menu).getByRole("menuitemradio", { name: "Все проекты" })).toHaveAttribute("aria-checked", "true");
    expect(within(menu).getByRole("menuitem", { name: "Новый проект" })).toBeInTheDocument();

    await user.click(within(menu).getByRole("menuitemradio", { name: /CRM/ }));
    await waitFor(() => expect(router.state.location.search).toMatchObject({ project: "CRM" }));
    await waitFor(() => expect(api.calls.some((c) => c.path === "/tasks" && c.query.get("project") === "p2")).toBe(true));
    expect(await screen.findByRole("button", { name: "Проект: CRM" })).toBeInTheDocument();
  });

  it("starts a new task in the project shown, with its queues and members", async () => {
    const user = userEvent.setup();
    const api = server({ "POST /tasks": () => json(201, task) });
    renderApp("/tasks/new?project=CRM", { api });
    const select = await screen.findByLabelText("Проект", undefined, firstLook);
    expect(select).toHaveValue("p2");
    // Only the project's queue; the projects without me are offered to admins too.
    await within(screen.getByLabelText("Очередь")).findByRole("option", { name: "Интеграции" });
    const queues = within(screen.getByLabelText("Очередь")).getAllByRole("option").map((o) => o.textContent);
    expect(queues).toEqual(["Без очереди", "Интеграции"]);
    expect(within(select).getByRole("option", { name: "Склад · WH" })).toBeInTheDocument();

    await user.type(screen.getByLabelText("Название"), "Импорт контактов");
    await user.click(screen.getByRole("button", { name: "Создать задачу" }));
    await waitFor(() => expect(api.calls.filter((c) => c.method === "POST" && c.path === "/tasks")).toHaveLength(1));
    expect(api.calls.find((c) => c.method === "POST" && c.path === "/tasks")?.body).toMatchObject({ title: "Импорт контактов", project_id: "p2" });
  });

  it("lets admins add a project with its key and access, and says when the key is taken", async () => {
    const user = userEvent.setup();
    let taken = true;
    const api = server({
      "POST /task-projects": () =>
        taken ? ((taken = false), problem(409, "project_exists")) : json(201, project("p4", { name: "Склад 2", key: "WHB", access: "team" })),
    });
    renderApp("/settings/tasks", { api });
    await user.click(await screen.findByRole("button", { name: "Добавить проект" }, firstLook));
    await user.click(screen.getByRole("button", { name: "Сохранить проект" }));
    expect(screen.getByLabelText("Название")).toHaveAccessibleDescription(/Напишите название проекта/);

    await user.type(screen.getByLabelText("Название"), "Склад 2");
    await user.type(screen.getByLabelText("Ключ"), "wh-b");
    expect(screen.getByLabelText("Ключ")).toHaveValue("WHB");
    await user.click(screen.getByRole("radio", { name: /Вся команда/ }));
    await user.click(screen.getByRole("button", { name: "Сохранить проект" }));
    expect(await screen.findByText("Проект с таким названием или ключом уже есть.")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Сохранить проект" }));
    await waitFor(() => expect(api.calls.filter((c) => c.method === "POST" && c.path === "/task-projects")).toHaveLength(2));
    expect(api.calls.filter((c) => c.method === "POST" && c.path === "/task-projects")[1]?.body).toEqual({
      name: "Склад 2",
      key: "WHB",
      color: "blue",
      access: "team",
      member_ids: [],
    });
  });

  it("archives a project with its whole state and explains why one with tasks stays", async () => {
    const user = userEvent.setup();
    const api = server({
      "PUT /task-projects/p2": () => json(200, { ...projects[1], archived_at: "2026-11-01T10:00:00Z" }),
      "DELETE /task-projects/p2": () => problem(409, "project_not_empty"),
    });
    renderApp("/settings/tasks", { api });
    const section = await screen.findByRole("region", { name: "Проекты" }, firstLook);
    const rows = await within(section).findAllByRole("listitem");
    const item = rows.find((li) => li.textContent?.startsWith("CRMCRM"))!;
    // The default project is never archived or deleted.
    const main = within(section).getByText("основной").closest("li")!;
    expect(within(main).queryByRole("button", { name: "В архив" })).not.toBeInTheDocument();

    await user.click(within(item).getByRole("button", { name: "В архив" }));
    await waitFor(() => expect(api.calls.filter((c) => c.method === "PUT")).toHaveLength(1));
    expect(api.calls.find((c) => c.method === "PUT")?.body).toEqual({
      name: "CRM",
      icon: null,
      color: "blue",
      access: "members",
      position: 1,
      archived: true,
      member_ids: ["u-anna"],
    });

    await user.click(within(item).getByRole("button", { name: "Удалить" }));
    expect(within(item).getByText("Удалить проект вместе с его очередями?")).toBeInTheDocument();
    await user.click(within(item).getByRole("button", { name: "Удалить" }));
    expect(await within(item).findByText("В проекте есть задачи — отправьте его в архив.")).toBeInTheDocument();
  });

  it("puts a new queue in the project picked", async () => {
    const user = userEvent.setup();
    const api = server({ "POST /task-queues": () => json(201, { id: "q-new", project_id: "p2", name: "Отчёты", member_ids: [], open_tasks: 0 }) });
    renderApp("/settings/tasks", { api });
    await user.click(await screen.findByRole("button", { name: "Добавить очередь" }, firstLook));
    await user.type(screen.getByLabelText("Название очереди"), "Отчёты");
    await user.selectOptions(screen.getByLabelText("Проект"), "p2");
    await user.click(screen.getByRole("button", { name: "Сохранить очередь" }));
    await waitFor(() => expect(api.calls.filter((c) => c.method === "POST" && c.path === "/task-queues")).toHaveLength(1));
    expect(api.calls.find((c) => c.method === "POST" && c.path === "/task-queues")?.body).toEqual({ name: "Отчёты", member_ids: [], project_id: "p2" });
  });
});
