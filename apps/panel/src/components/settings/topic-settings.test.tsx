import type { Topic } from "@tolkovo/api-client";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { fakeApi, json, operatorSession, ownerSession, problem } from "../../test/fake-api";
import { renderApp } from "../../test/render-app";

const admin = { ...ownerSession, permissions: [...ownerSession.permissions, "routing.manage", "tasks.view"] };

const topic = (id: string, name: string, patch: Partial<Topic> = {}): Topic => ({
  id,
  name,
  color: "gray",
  position: 0,
  priority: 0,
  task_queue_id: null,
  member_ids: [],
  ...patch,
});

describe("topic settings", () => {
  it("adds a topic with its priority, members and task queue", async () => {
    const user = userEvent.setup();
    let topics: Topic[] = [];
    const api = fakeApi({
      session: admin,
      routes: {
        "GET /topics": () => json(200, { items: topics }),
        "GET /task-queues": () => json(200, { items: [{ id: "q-crm", name: "CRM", member_ids: [], open_tasks: 0 }] }),
        "POST /topics": async (r) => {
          const body = (await r.json()) as Topic;
          topics = [topic("t1", body.name, { ...body, position: 0 })];
          return json(201, topics[0]);
        },
      },
    });
    renderApp("/settings/topics", { api });
    expect(await screen.findByRole("heading", { level: 2, name: "Темы вопросов" })).toBeInTheDocument();
    expect(await screen.findByText(/Тем пока нет/)).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Добавить тему" }));
    await user.click(screen.getByRole("button", { name: "Сохранить тему" }));
    expect(screen.getByLabelText("Название")).toHaveAccessibleDescription(/Напишите название темы/);

    await user.type(screen.getByLabelText("Название"), "CRM");
    await user.click(screen.getByRole("radio", { name: "Синий" }));
    await user.selectOptions(screen.getByLabelText("Приоритет"), "2");
    await user.click(screen.getByRole("checkbox", { name: "Анна Смирнова" }));
    await user.selectOptions(screen.getByLabelText("Очередь задач"), "q-crm");
    await user.click(screen.getByRole("button", { name: "Сохранить тему" }));
    await waitFor(() => expect(api.calls.filter((c) => c.method === "POST")).toHaveLength(1));
    expect(api.calls.find((c) => c.method === "POST")?.body).toEqual({
      name: "CRM",
      color: "blue",
      priority: 2,
      task_queue_id: "q-crm",
      member_ids: ["u-anna"],
    });
    const item = (await screen.findByText("CRM", { selector: "span" })).closest("li")!;
    expect(within(item).getByText(/Высокий приоритет · Анна Смирнова · задачи → CRM/)).toBeInTheDocument();
  });

  it("reorders and deletes topics after a confirmation", async () => {
    const user = userEvent.setup();
    let topics = [topic("t1", "CRM"), topic("t2", "OnlyFans", { position: 1 })];
    const api = fakeApi({
      session: admin,
      routes: {
        "GET /topics": () => json(200, { items: topics }),
        "GET /task-queues": () => json(200, { items: [] }),
        "PUT /topics/order": async (r) => {
          const { ids } = (await r.json()) as { ids: string[] };
          topics = ids.map((id, i) => ({ ...topics.find((x) => x.id === id)!, position: i }));
          return json(200, { items: topics });
        },
        "DELETE /topics/t1": () => {
          topics = topics.filter((x) => x.id !== "t1");
          return new Response(null, { status: 204 });
        },
      },
    });
    renderApp("/settings/topics", { api });
    await user.click(await screen.findByRole("button", { name: "Опустить «CRM»" }));
    await waitFor(() => expect(api.calls.find((c) => c.method === "PUT")?.body).toEqual({ ids: ["t2", "t1"] }));
    expect(screen.getByRole("button", { name: "Поднять «OnlyFans»" })).toBeDisabled();

    const crm = screen.getByText("CRM", { selector: "span" }).closest("li")!;
    await user.click(within(crm).getByRole("button", { name: "Удалить" }));
    expect(within(crm).getByText(/Диалоги и задачи останутся без темы/)).toBeInTheDocument();
    await user.click(within(crm).getByRole("button", { name: "Удалить тему" }));
    await waitFor(() => expect(screen.queryByText("CRM", { selector: "span" })).not.toBeInTheDocument());
  });

  it("explains a taken name at the field", async () => {
    const user = userEvent.setup();
    renderApp("/settings/topics", {
      api: fakeApi({
        session: admin,
        routes: {
          "GET /task-queues": () => json(200, { items: [] }),
          "POST /topics": () => problem(409, "topic_exists"),
        },
      }),
    });
    await user.click(await screen.findByRole("button", { name: "Добавить тему" }));
    await user.type(screen.getByLabelText("Название"), "CRM");
    await user.click(screen.getByRole("button", { name: "Сохранить тему" }));
    await waitFor(() => expect(screen.getByLabelText("Название")).toHaveAccessibleDescription(/Тема с таким названием уже есть/));
  });

  it("is not in the tree without routing.manage", async () => {
    renderApp("/settings/team", { api: fakeApi({ session: operatorSession }) });
    await screen.findByRole("heading", { level: 2, name: "Команда" });
    expect(screen.queryByRole("link", { name: "Темы вопросов" })).not.toBeInTheDocument();
  });
});
