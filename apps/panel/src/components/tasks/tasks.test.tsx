import type { ConversationTasks, Session, Task, TaskDraft } from "@tolkovo/api-client";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it } from "vitest";

import { resetDrafts } from "../../lib/inbox/drafts";
import { fakeApi, json, ownerSession, type FakeRoutes } from "../../test/fake-api";
import { contact, conv, inboxOperator, msg } from "../../test/inbox-fixtures";
import { renderApp } from "../../test/render-app";

/** The tasks section (ADR-0015): every member works on tasks, admins manage the section. */

const member: Session = { ...inboxOperator, permissions: [...inboxOperator.permissions, "tasks.edit", "tasks.view"] };
const admin: Session = { ...ownerSession, permissions: [...ownerSession.permissions, "tasks.edit", "tasks.manage", "tasks.view"] };

const task = (id: string, patch: Partial<Task> = {}): Task => ({
  id,
  number: 12,
  queue_id: null,
  title: "Обновить сертификат на сервере",
  body: "Истекает в пятницу",
  status: "open",
  priority: "normal",
  assignee_id: null,
  created_by: admin.user.id,
  created_at: "2026-10-06T10:00:00Z",
  updated_at: "2026-10-06T10:00:00Z",
  closed_at: null,
  version: 3,
  conversation_ids: [],
  ...patch,
});

const settings = { enabled: true, conversation_button: "both", reply_ru: "Готово", reply_en: "Done" } as const;

function server(session: Session, routes: FakeRoutes = {}) {
  const state = { task: task("t1") };
  const api = fakeApi({
    session,
    routes: {
      "GET /tasks": () => json(200, { items: [state.task], next_cursor: null }),
      "GET /tasks/summary": () => json(200, { assigned_open: 0 }),
      "GET /tasks/t1": () => json(200, state.task),
      "GET /tasks/t1/activity": () => json(200, { items: [{ id: "a1", actor_id: admin.user.id, kind: "created", created_at: "2026-10-06T10:00:00Z" }] }),
      "GET /task-queues": () => json(200, { items: [{ id: "q-dev", name: "Разработка", member_ids: [], open_tasks: 1 }] }),
      "GET /task-settings": () => json(200, { settings, defaults: { reply_ru: "Готово", reply_en: "Done" }, placeholders: ["name", "task_number"] }),
      ...routes,
    },
  });
  return { api, state };
}

const calls = (api: ReturnType<typeof fakeApi>, method: string, path: string) => api.calls.filter((c) => c.method === method && c.path === path);

describe("the tasks section", () => {
  afterEach(() => resetDrafts());

  it("is in the menu for members, with the list and the open task", async () => {
    const user = userEvent.setup();
    const { api, state } = server(member, {
      "PUT /tasks/t1": async (request) => {
        const body = (await request.json()) as Task;
        state.task = { ...state.task, ...body, version: state.task.version + 1 };
        return json(200, state.task);
      },
    });
    renderApp("/tasks?view=open", { api });
    await user.click(await screen.findByRole("link", { name: /Обновить сертификат на сервере/ }));
    const heading = await screen.findByRole("heading", { name: /З-12.*Обновить сертификат на сервере/ });
    expect(heading).toHaveFocus();
    expect(screen.getByText("Истекает в пятницу")).toBeInTheDocument();
    expect(await screen.findByText(/Задача создана/)).toBeInTheDocument();

    // "Take it" gives the task to me and puts it in progress, at the version read.
    await user.click(screen.getByRole("button", { name: "Взять в работу" }));
    await waitFor(() => expect(calls(api, "PUT", "/tasks/t1")).toHaveLength(1));
    expect(calls(api, "PUT", "/tasks/t1")[0]?.body).toEqual({
      version: 3,
      title: "Обновить сертификат на сервере",
      body: "Истекает в пятницу",
      status: "in_progress",
      priority: "normal",
      queue_id: null,
      assignee_id: member.user.id,
    });
    // Members do not delete tasks.
    expect(screen.queryByRole("button", { name: "Удалить задачу" })).not.toBeInTheDocument();
  });

  it("creates a task by hand in a queue", async () => {
    const user = userEvent.setup();
    const { api } = server(member, {
      "POST /tasks": () => json(201, task("t1")),
    });
    renderApp("/tasks", { api });
    await user.click(await screen.findByRole("link", { name: "Новая" }));
    await user.type(await screen.findByLabelText("Название"), "Принтер на втором этаже");
    await user.selectOptions(screen.getByLabelText("Очередь"), "q-dev");
    await user.selectOptions(screen.getByLabelText("Приоритет"), "high");
    expect(screen.getByText(/участники очереди/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Создать задачу" }));
    await waitFor(() => expect(calls(api, "POST", "/tasks")).toHaveLength(1));
    expect(calls(api, "POST", "/tasks")[0]?.body).toEqual({
      title: "Принтер на втором этаже",
      body: "",
      priority: "high",
      queue_id: "q-dev",
      assignee_id: null,
    });
    expect(await screen.findByRole("heading", { name: /З-12/ })).toBeInTheDocument();
  });

  it("lets admins add a queue in the settings", async () => {
    const user = userEvent.setup();
    const { api } = server(admin, {
      "POST /task-queues": () => json(201, { id: "q-it", name: "Инфраструктура", member_ids: [], open_tasks: 0 }),
    });
    renderApp("/settings/tasks", { api });
    await user.click(await screen.findByRole("button", { name: "Добавить очередь" }));
    await user.type(screen.getByLabelText("Название очереди"), "Инфраструктура");
    await user.click(screen.getByRole("button", { name: "Сохранить очередь" }));
    await waitFor(() => expect(calls(api, "POST", "/task-queues")).toHaveLength(1));
    expect(calls(api, "POST", "/task-queues")[0]?.body).toEqual({ name: "Инфраструктура", member_ids: [] });
  });
});

describe("tasks in a conversation", () => {
  afterEach(() => resetDrafts());

  const draft: TaskDraft = {
    title: "Корзина пустеет после обновления",
    body: "После обновления корзина пустая.\n\nСообщение клиента:\nКорзина пустая",
    drafted_by: "ai",
  };

  function inbox(conversationTasks: ConversationTasks, extra: FakeRoutes = {}) {
    return fakeApi({
      session: member,
      routes: {
        "GET /conversations": () => json(200, { items: [conv("c1")], next_cursor: null }),
        "GET /conversations/c1": () => json(200, conv("c1")),
        "GET /conversations/c1/messages": () => json(200, { items: [msg("m1", { body: "Корзина пустая" })], next_cursor: null }),
        "GET /contacts/k-c1": () => json(200, contact("k-c1")),
        "POST /conversations/c1/read": () => new Response(null, { status: 204 }),
        "GET /conversations/c1/tasks": () => json(200, conversationTasks),
        "GET /conversations/c1/tasks/draft": () => json(200, draft),
        "GET /task-queues": () => json(200, { items: [] }),
        "GET /task-settings": () => json(200, { settings, defaults: { reply_ru: "Готово", reply_en: "Done" }, placeholders: [] }),
        "GET /tasks/summary": () => json(200, { assigned_open: 0 }),
        ...extra,
      },
    });
  }

  it("creates a task from the AI draft, linked to the conversation", async () => {
    const user = userEvent.setup();
    const api = inbox({ items: [], settings, reply_draft: null }, { "POST /tasks": () => json(201, task("t1", { conversation_ids: ["c1"] })) });
    renderApp("/inbox/c1", { api });
    await user.click(await screen.findByRole("button", { name: "Ещё действия" }));
    await user.click(await screen.findByRole("menuitem", { name: "Создать задачу" }));
    const form = await screen.findByRole("region", { name: "Новая задача" });
    expect(await within(form).findByText(/Черновик написал ИИ/)).toBeInTheDocument();
    expect(within(form).getByLabelText("Название")).toHaveValue(draft.title);
    expect(within(form).getByLabelText("Описание")).toHaveValue(draft.body);
    await user.click(within(form).getByRole("button", { name: "Создать задачу" }));
    await waitFor(() => expect(calls(api, "POST", "/tasks")).toHaveLength(1));
    expect(calls(api, "POST", "/tasks")[0]?.body).toEqual({
      title: draft.title,
      body: draft.body,
      priority: "normal",
      queue_id: null,
      assignee_id: null,
      conversation_id: "c1",
    });
    expect(await screen.findByText("Задача З-12 создана. Диалог ждёт, пока её не закроют.")).toBeInTheDocument();
  });

  it("offers the reply about a done task and no task button when the workspace keeps only GitHub", async () => {
    const user = userEvent.setup();
    const api = inbox({
      items: [task("t1", { status: "done", closed_at: "2026-10-06T11:00:00Z", conversation_ids: ["c1"] })],
      settings: { enabled: true, conversation_button: "github" },
      reply_draft: { task_id: "t1", number: 12, text: "Марина, задачу выполнили." },
    });
    renderApp("/inbox/c1", { api });
    expect(await screen.findByText("Задача З-12 выполнена. Черновик ответа:")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Вставить в ответ" }));
    expect(screen.getByLabelText("Ответ клиенту")).toHaveValue("Марина, задачу выполнили.");
    await user.click(screen.getByRole("button", { name: "Ещё действия" }));
    expect(screen.queryByRole("menuitem", { name: "Создать задачу" })).not.toBeInTheDocument();
  });
});
