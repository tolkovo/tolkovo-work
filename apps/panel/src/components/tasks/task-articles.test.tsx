import type { KBArticle, Session, Task, TaskArticle, TaskBrief, TaskProject } from "@tolkovo/api-client";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it } from "vitest";

import { resetDrafts } from "../../lib/inbox/drafts";
import { fakeApi, json, ownerSession, type FakeRoutes } from "../../test/fake-api";
import { contact, conv, inboxOperator, msg } from "../../test/inbox-fixtures";
import { renderApp } from "../../test/render-app";

/** The customer's tasks and the articles of tasks (ADR-0022, P5). */

const admin: Session = { ...ownerSession, permissions: [...ownerSession.permissions, "tasks.edit", "tasks.manage", "tasks.view", "knowledge.manage"] };
const member: Session = { ...inboxOperator, permissions: [...inboxOperator.permissions, "tasks.edit", "tasks.view"] };

const task: Task = {
  id: "t1",
  project_id: "p1",
  project_key: "TSK",
  number: 1,
  ref: "TSK-1",
  queue_id: null,
  topic_id: null,
  title: "Не проходит оплата",
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
};

const entry = (id: string, title: string, patch: Partial<KBArticle> = {}): KBArticle => ({
  id,
  kind: "article",
  title,
  body: "Текст статьи.",
  url: null,
  published: true,
  topic_ids: [],
  created_at: "2026-10-07T10:00:00Z",
  updated_at: "2026-10-07T10:00:00Z",
  ...patch,
});

const linked = (a: KBArticle): TaskArticle => ({ id: a.id, kind: a.kind, title: a.title, published: a.published, linked_at: "2026-11-11T10:00:00Z" });

const brief = (id: string, ref: string, title: string, projectId = "p1"): TaskBrief => ({
  id,
  project_id: projectId,
  ref,
  title,
  status: "open",
  assignee_id: null,
  updated_at: "2026-11-11T10:00:00Z",
});

const project = (id: string, name: string, key: string): TaskProject => ({
  id,
  name,
  key,
  former_keys: [],
  icon: null,
  color: "blue",
  access: "team",
  is_default: key === "TSK",
  position: 0,
  member_ids: [],
  open_tasks: 1,
  archived_at: null,
  member: true,
});

function taskServer(session: Session, routes: FakeRoutes = {}) {
  return fakeApi({
    session,
    routes: {
      "GET /tasks": () => json(200, { items: [task], next_cursor: null }),
      "GET /tasks/summary": () => json(200, { assigned_open: 0, projects: [] }),
      "GET /tasks/t1": () => json(200, task),
      "GET /tasks/t1/activity": () => json(200, { items: [] }),
      "GET /task-queues": () => json(200, { items: [] }),
      "GET /task-board-settings": () => json(200, { rules: [] }),
      "GET /task-settings": () =>
        json(200, {
          settings: { enabled: true, conversation_button: "both", reply_ru: "Готово", reply_en: "Done" },
          defaults: { reply_ru: "Готово", reply_en: "Done" },
          placeholders: [],
        }),
      ...routes,
    },
  });
}

const firstLook = { timeout: 3000 };

describe("articles of a task", () => {
  it("links an article found by its title and unlinks it", async () => {
    const user = userEvent.setup();
    const refunds = entry("a1", "Оплата и возвраты");
    const draft = entry("a2", "Оплата картой", { published: false });
    let items: TaskArticle[] = [];
    const api = taskServer(admin, {
      "GET /kb/articles": () => json(200, { items: [refunds, draft, entry("a3", "Импорт контактов")] }),
      "GET /tasks/t1/articles": () => json(200, { items }),
      "POST /tasks/t1/articles": () => {
        items = [linked(refunds)];
        return json(201, { items });
      },
      "DELETE /tasks/t1/articles/a1": () => {
        items = [];
        return new Response(null, { status: 204 });
      },
    });
    renderApp("/tasks/t1", { api });
    const section = await screen.findByRole("region", { name: "Статьи" }, firstLook);
    expect(await within(section).findByText("Статьи базы знаний не привязаны.")).toBeInTheDocument();

    await user.click(within(section).getByRole("button", { name: "Привязать статью" }));
    await user.type(within(section).getByLabelText("Статья базы знаний"), "оплата");
    const found = within(section).getByRole("list", { name: "Найденные статьи" });
    expect(within(found).getAllByRole("button").map((b) => b.textContent)).toEqual(["Оплата и возвраты", "Оплата картойчерновик"]);
    await user.click(within(found).getByRole("button", { name: "Оплата и возвраты" }));
    await waitFor(() => expect(api.calls.find((c) => c.method === "POST" && c.path === "/tasks/t1/articles")?.body).toEqual({ article_id: "a1" }));

    const link = await within(section).findByRole("link", { name: "Оплата и возвраты" });
    expect(link).toHaveAttribute("href", "/settings/knowledge#kb-article-a1");
    await user.click(within(section).getByRole("button", { name: "Отвязать статью «Оплата и возвраты»" }));
    await waitFor(() => expect(api.calls.some((c) => c.method === "DELETE" && c.path === "/tasks/t1/articles/a1")).toBe(true));
    expect(await within(section).findByText("Статьи базы знаний не привязаны.")).toBeInTheDocument();
  });

  it("shows the titles to a member without the knowledge base, with nothing to change", async () => {
    const api = taskServer(member, {
      "GET /tasks/t1/articles": () => json(200, { items: [linked(entry("a1", "Оплата и возвраты", { published: false }))] }),
    });
    renderApp("/tasks/t1", { api });
    const section = await screen.findByRole("region", { name: "Статьи" }, firstLook);
    expect(await within(section).findByText("Оплата и возвраты")).toBeInTheDocument();
    expect(within(section).getByText("черновик")).toBeInTheDocument();
    expect(within(section).queryByRole("link")).not.toBeInTheDocument();
    expect(within(section).queryByRole("button")).not.toBeInTheDocument();
  });

  it("is not there for a member without the base while the task has no articles", async () => {
    renderApp("/tasks/t1", { api: taskServer(member) });
    expect(await screen.findByRole("region", { name: "Связи" }, firstLook)).toBeInTheDocument();
    expect(screen.queryByRole("region", { name: "Статьи" })).not.toBeInTheDocument();
  });
});

describe("tasks of an article", () => {
  it("unfolds under the entry of the base, a hidden task only counted", async () => {
    const user = userEvent.setup();
    const api = fakeApi({
      session: admin,
      routes: {
        "GET /kb/guide": () => json(200, { about: "", handoff_rules: "", task_checklist: "", updated_at: null }),
        "GET /kb/articles": () => json(200, { items: [entry("a1", "Оплата и возвраты")] }),
        "GET /kb/articles/a1/tasks": () => json(200, { items: [brief("t1", "CRM-41", "Возврат не дошёл")], hidden: 2 }),
      },
    });
    renderApp("/settings/knowledge", { api });
    const toggle = await screen.findByRole("button", { name: "Задачи по статье «Оплата и возвраты»" }, firstLook);
    expect(api.calls.some((c) => c.path === "/kb/articles/a1/tasks")).toBe(false);
    await user.click(toggle);
    expect(toggle).toHaveAttribute("aria-expanded", "true");
    expect(await screen.findByRole("link", { name: "CRM-41 Возврат не дошёл" })).toHaveAttribute("href", "/tasks/t1");
    expect(screen.getByText("Ещё задач в проектах без вашего участия: 2")).toBeInTheDocument();
  });
});

describe("the customer's tasks in the card", () => {
  afterEach(() => resetDrafts());

  function inbox(session: Session, routes: FakeRoutes = {}) {
    return fakeApi({
      session,
      routes: {
        "GET /conversations": () => json(200, { items: [conv("c1")], next_cursor: null }),
        "GET /conversations/c1": () => json(200, conv("c1")),
        "GET /conversations/c1/messages": () => json(200, { items: [msg("m1", { body: "Опять не проходит оплата" })], next_cursor: null }),
        "GET /contacts/k-c1": () => json(200, contact("k-c1")),
        "POST /conversations/c1/read": () => new Response(null, { status: 204 }),
        "GET /conversations/c1/tasks": () =>
          json(200, {
            items: [],
            hidden_count: 0,
            settings: { enabled: true, conversation_button: "both", reply_ru: "Готово", reply_en: "Done" },
            reply_draft: null,
          }),
        "GET /task-queues": () => json(200, { items: [] }),
        "GET /tasks/summary": () => json(200, { assigned_open: 0, projects: [] }),
        ...routes,
      },
    });
  }

  it("groups them by project and counts the hidden ones", async () => {
    const api = inbox(member, {
      "GET /task-projects": () => json(200, { items: [project("p1", "Задачи", "TSK"), project("p2", "CRM", "CRM")] }),
      "GET /contacts/k-c1/tasks": () =>
        json(200, { items: [brief("t2", "CRM-3", "Импорт контактов", "p2"), brief("t1", "TSK-1", "Не проходит оплата")], hidden: 1 }),
    });
    renderApp("/inbox/c1", { api });
    const crm = await screen.findByRole("list", { name: "CRM" }, firstLook);
    expect(within(crm).getByRole("link", { name: "CRM-3 Импорт контактов" })).toHaveAttribute("href", "/tasks/t2");
    expect(within(screen.getByRole("list", { name: "Задачи" })).getByRole("link", { name: "TSK-1 Не проходит оплата" })).toBeInTheDocument();
    expect(screen.getByText("Ещё задач в проектах без вашего участия: 1")).toBeInTheDocument();
  });

  it("is not there without the contacts", async () => {
    const programmer: Session = { ...member, permissions: member.permissions.filter((p) => !p.startsWith("contacts.")) };
    const api = inbox(programmer);
    renderApp("/inbox/c1", { api });
    expect(await screen.findByText("Опять не проходит оплата", {}, firstLook)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Задачи по клиенту/ })).not.toBeInTheDocument();
    expect(api.calls.some((c) => c.path === "/contacts/k-c1/tasks")).toBe(false);
  });
});
