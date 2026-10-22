import type { Session, Task, TaskBoard, TaskStatus } from "@tolkovo/api-client";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it } from "vitest";

import { TASKS_LAYOUT_KEY } from "../../lib/preferences";
import { fakeApi, json, ownerSession, problem } from "../../test/fake-api";
import { inboxOperator } from "../../test/inbox-fixtures";
import { renderApp } from "../../test/render-app";

/** The board of tasks (ADR-0019): columns by status; cards move by keys, the menu and dragging. */

const member: Session = { ...inboxOperator, permissions: [...inboxOperator.permissions, "tasks.edit", "tasks.view"] };

const task = (number: number, patch: Partial<Task> = {}): Task => ({
  id: `t${number}`,
  number,
  queue_id: null,
  topic_id: null,
  title: `Задача ${number}`,
  body: "",
  status: "open",
  priority: "normal",
  assignee_id: null,
  created_by: ownerSession.user.id,
  created_at: "2026-10-01T10:00:00Z",
  updated_at: "2026-10-01T10:00:00Z",
  closed_at: null,
  status_changed_at: "2026-10-10T10:00:00Z",
  blocked_reason: null,
  version: 1,
  conversation_ids: [],
  conversation_count: 0,
  files: [],
  tag_ids: [],
  ...patch,
});

const STATUSES: TaskStatus[] = ["open", "in_progress", "review", "verified", "done"];

type Rules = Partial<Record<TaskStatus, { wip_limit: number | null; age_days: number | null }>>;

function boardOf(tasks: Task[], rules: Rules = {}): TaskBoard {
  return {
    columns: STATUSES.map((status) => {
      const items = tasks.filter((x) => x.status === status);
      return { status, count: items.length, items, next_cursor: null, wip_limit: null, age_days: null, ...rules[status] };
    }),
  };
}

function server(tasks: Task[], put?: (body: Task) => Response, rules: Rules = {}) {
  const state = { tasks };
  const api = fakeApi({
    session: member,
    routes: {
      "GET /tasks/board": () => json(200, boardOf(state.tasks, rules)),
      "GET /tasks/summary": () => json(200, { assigned_open: 0 }),
      "GET /task-queues": () => json(200, { items: [] }),
      ...Object.fromEntries(
        tasks.map((x) => [
          `PUT /tasks/${x.id}`,
          async (request: Request) => {
            const body = (await request.json()) as Task;
            if (put) return put(body);
            const changed = { ...x, ...body, version: x.version + 1, status_changed_at: "2026-10-22T10:00:00Z" };
            state.tasks = state.tasks.map((y) => (y.id === x.id ? changed : y));
            return json(200, changed);
          },
        ]),
      ),
    },
  });
  return { api, state };
}

const firstLook = { timeout: 3000 };
const column = (name: RegExp) => screen.getByRole("region", { name });
const puts = (api: ReturnType<typeof fakeApi>, id: string) => api.calls.filter((c) => c.method === "PUT" && c.path === `/tasks/${id}`);

describe("the board of tasks", () => {
  afterEach(() => localStorage.clear());

  it("shows a column by status, the urgent first, and remembers the view", async () => {
    const { api } = server([
      task(1, { status_changed_at: "2026-10-05T10:00:00Z" }),
      task(2, { priority: "urgent", conversation_count: 3 }),
      task(3, { status: "in_progress", assignee_id: member.user.id }),
    ]);
    renderApp("/tasks?layout=board", { api });
    const open = await screen.findByRole("region", { name: /^Новые/ }, firstLook);
    const cards = within(open).getAllByRole("link", { name: /^#\d+/ });
    expect(cards.map((c) => c.getAttribute("aria-label"))).toEqual(["#2, Задача 2", "#1, Задача 1"]);
    // "Urgent" heads the column and marks the card.
    expect(within(open).getAllByText("Срочно")).toHaveLength(2);
    expect(within(open).getAllByText(/3 клиента ждут эту задачу/).length).toBeGreaterThan(0);
    for (const name of [/^В работе/, /^На проверке/, /^Проверено/, /^Готово/]) expect(column(name)).toBeInTheDocument();
    expect(within(column(/^На проверке/)).getByText("Ничего не ждёт проверки")).toBeInTheDocument();

    await userEvent.setup().click(screen.getByRole("link", { name: "Список" }));
    expect(localStorage.getItem(TASKS_LAYOUT_KEY)).toBeNull();
  });

  it("moves a card to the next column by Shift+→ and says so", async () => {
    const user = userEvent.setup();
    const { api } = server([task(1), task(2, { status: "in_progress" })]);
    renderApp("/tasks?layout=board", { api });
    const card = await screen.findByRole("link", { name: "#1, Задача 1" }, firstLook);
    card.focus();
    await user.keyboard("{Shift>}{ArrowRight}{/Shift}");
    await waitFor(() => expect(puts(api, "t1")).toHaveLength(1));
    // In progress without an assignee: the server gives it to whoever moved it.
    expect(puts(api, "t1")[0]?.body).toMatchObject({ version: 1, status: "in_progress", assignee_id: null });
    expect(await within(column(/^В работе/)).findByRole("link", { name: "#1, Задача 1" })).toBeInTheDocument();
    expect(await screen.findByText("#1 — в колонке «В работе»")).toBeInTheDocument();
  });

  it("walks the cards with the arrows and moves one by its menu", async () => {
    const user = userEvent.setup();
    const { api } = server([task(1), task(2, { status: "review" })]);
    renderApp("/tasks?layout=board", { api });
    const first = await screen.findByRole("link", { name: "#1, Задача 1" }, firstLook);
    first.focus();
    await user.keyboard("{ArrowRight}");
    expect(screen.getByRole("link", { name: "#2, Задача 2" })).toHaveFocus();

    await user.keyboard("m");
    const menu = await screen.findByRole("menu");
    // The next step leads the menu.
    const items = within(menu).getAllByRole("menuitem");
    expect(items[0]).toHaveTextContent("В «Проверено»");
    await user.click(items[0] as HTMLElement);
    await waitFor(() => expect(puts(api, "t2")[0]?.body).toMatchObject({ status: "verified" }));
  });

  it("asks before done when customers wait, and sends nothing on cancel", async () => {
    const user = userEvent.setup();
    const { api } = server([task(5, { status: "verified", conversation_count: 2 })]);
    renderApp("/tasks?layout=board", { api });
    const card = await screen.findByRole("link", { name: "#5, Задача 5" }, firstLook);
    card.focus();
    await user.keyboard("{Shift>}{ArrowRight}{/Shift}");
    let dialog = await screen.findByRole("dialog", { name: "Задача в проде?" });
    expect(dialog).toHaveTextContent("2 диалога вернутся исполнителям с черновиком ответа");
    await user.click(within(dialog).getByRole("button", { name: "Отмена" }));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(puts(api, "t5")).toHaveLength(0);

    card.focus();
    await user.keyboard("{Shift>}{ArrowRight}{/Shift}");
    dialog = await screen.findByRole("dialog", { name: "Задача в проде?" });
    await user.click(within(dialog).getByRole("button", { name: "Готово" }));
    await waitFor(() => expect(puts(api, "t5")[0]?.body).toMatchObject({ status: "done" }));
  });

  it("puts the card back when the task changed meanwhile", async () => {
    const user = userEvent.setup();
    const { api } = server([task(1)], () => problem(409, "task_conflict"));
    renderApp("/tasks?layout=board", { api });
    const card = await screen.findByRole("link", { name: "#1, Задача 1" }, firstLook);
    card.focus();
    await user.keyboard("{Shift>}{ArrowRight}{/Shift}");
    expect(await screen.findByRole("alert")).toHaveTextContent("Задачу успели изменить");
    expect(within(column(/^Новые/)).getByRole("link", { name: "#1, Задача 1" })).toBeInTheDocument();
  });

  it("says when there are no tasks, and offers to retry after an error", async () => {
    const user = userEvent.setup();
    const empty = server([]);
    const { unmount } = renderApp("/tasks?layout=board", { api: empty.api });
    expect(await screen.findByRole("heading", { name: "Задач пока нет" }, firstLook)).toBeInTheDocument();
    unmount();

    let fail = true;
    const api = fakeApi({
      session: member,
      routes: {
        "GET /tasks/board": () => (fail ? problem(403, "forbidden") : json(200, boardOf([task(1)]))),
        "GET /tasks/summary": () => json(200, { assigned_open: 0 }),
        "GET /task-queues": () => json(200, { items: [] }),
      },
    });
    renderApp("/tasks?layout=board", { api });
    expect(await screen.findByText("Не удалось загрузить доску.", undefined, firstLook)).toBeInTheDocument();
    fail = false;
    await user.click(screen.getByRole("button", { name: "Повторить" }));
    expect(await screen.findByRole("link", { name: "#1, Задача 1" })).toBeInTheDocument();
  });

  it("shows the soft limit, the cards standing too long and the blocked ones", async () => {
    const { api } = server(
      [
        task(1, { status: "in_progress", status_changed_at: "2026-10-01T10:00:00Z" }),
        task(2, { status: "in_progress", blocked_reason: "Ждём доступ к стенду" }),
      ],
      undefined,
      { in_progress: { wip_limit: 1, age_days: 3 } },
    );
    renderApp("/tasks?layout=board", { api });
    const work = await screen.findByRole("region", { name: /^В работе/ }, firstLook);
    expect(within(work).getByTitle("Больше лимита (1): сначала закончить начатое")).toHaveTextContent("2/1");
    const card = within(work).getByRole("link", { name: "#2, Задача 2" }).closest("[data-task-id]") as HTMLElement;
    expect(within(card).getByText("Заблокирована")).toBeInTheDocument();
    expect(within(card).getByTitle("Ждём доступ к стенду")).toBeInTheDocument();
    const old = within(work).getByRole("link", { name: "#1, Задача 1" }).closest("[data-task-id]") as HTMLElement;
    expect(within(old).getByText(/Стоит дольше порога: 3 д/)).toBeInTheDocument();
  });

  it("blocks a task from its menu with a reason, and unblocks it", async () => {
    const user = userEvent.setup();
    const { api } = server([task(1, { status: "in_progress" })]);
    renderApp("/tasks?layout=board", { api });
    await screen.findByRole("link", { name: "#1, Задача 1" }, firstLook);
    await user.click(screen.getByRole("button", { name: "Действия с задачей #1" }));
    await user.click(screen.getByRole("menuitem", { name: "Заблокировать…" }));
    const dialog = screen.getByRole("dialog", { name: "Заблокировать задачу #1" });
    const submit = within(dialog).getByRole("button", { name: "Заблокировать" });
    expect(submit).toBeDisabled();
    await user.type(within(dialog).getByLabelText("Что мешает"), "Ждём ответа бухгалтерии");
    await user.click(submit);
    await waitFor(() => expect(puts(api, "t1")[0]?.body).toMatchObject({ status: "in_progress", blocked_reason: "Ждём ответа бухгалтерии" }));
    expect(await screen.findByText("#1 заблокирована")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Действия с задачей #1" }));
    await user.click(screen.getByRole("menuitem", { name: "Снять блокировку" }));
    await waitFor(() => expect(puts(api, "t1")[1]?.body).toMatchObject({ blocked_reason: "" }));
  });

  it("groups the cards in swimlanes that fold", async () => {
    const user = userEvent.setup();
    const { api } = server([task(1, { priority: "urgent" }), task(2, { status: "review", priority: "low" }), task(3, { priority: "low" })]);
    renderApp("/tasks?layout=board&lanes=priority", { api });
    const urgent = await screen.findByRole("button", { name: /^Срочно\s*1$/ }, firstLook);
    const low = screen.getByRole("button", { name: /^Низкий\s*2$/ });
    expect(urgent.compareDocumentPosition(low) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(screen.getByRole("link", { name: "#3, Задача 3" })).toBeInTheDocument();
    await user.click(low);
    expect(low).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByRole("link", { name: "#3, Задача 3" })).not.toBeInTheDocument();
    expect(screen.getByRole("link", { name: "#1, Задача 1" })).toBeInTheDocument();
  });
});

