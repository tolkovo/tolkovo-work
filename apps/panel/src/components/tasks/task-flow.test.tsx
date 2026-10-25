import type { Session, TaskFlow } from "@tolkovo/api-client";
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { fakeApi, json } from "../../test/fake-api";
import { inboxOperator } from "../../test/inbox-fixtures";
import { renderApp } from "../../test/render-app";

/** «Поток» of the tasks section (ADR-0019, K3): cycle time, weekly throughput, statuses by day, aging. */

const member: Session = { ...inboxOperator, permissions: [...inboxOperator.permissions, "tasks.edit", "tasks.view"] };

const day = (n: number) => `2026-10-${String(n).padStart(2, "0")}`;

const flow: TaskFlow = {
  since: day(16),
  until: day(22),
  cycle: [{ task_id: "t1", number: 1, title: "Вылет при входе", done_at: "2026-10-20T10:00:00Z", days: 2 }],
  cycle_p50: 2,
  cycle_p85: 2,
  throughput: [
    { week: day(12), done: 1 },
    { week: day(19), done: 1 },
  ],
  cfd: [16, 17, 18, 19, 20, 21, 22].map((n) => ({ day: day(n), open: 3, in_progress: n >= 19 ? 1 : 0, review: 0, verified: 0, done: n >= 20 ? 2 : 1 })),
  aging: [{ task_id: "t4", number: 4, title: "Отчёт по сменам", status: "open", days: 21.1, blocked: true }],
  truncated: false,
};

function server(answer: TaskFlow) {
  const queries: string[] = [];
  const api = fakeApi({
    session: member,
    routes: {
      "GET /tasks/flow": (request: Request) => {
        queries.push(new URL(request.url).search);
        return json(200, answer);
      },
      "GET /tasks/summary": () => json(200, { assigned_open: 0 }),
      "GET /task-queues": () => json(200, { items: [] }),
    },
  });
  return { api, queries };
}

const firstLook = { timeout: 3000 };

describe("the flow of tasks", () => {
  it("shows the cycle time, the weeks, the statuses and the oldest open tasks", async () => {
    const { api, queries } = server(flow);
    renderApp("/tasks?layout=flow", { api });
    expect(await screen.findByRole("heading", { level: 2, name: "Время цикла" }, firstLook)).toBeInTheDocument();
    expect(screen.getByText("От «В работе» до «Готово», задач: 1. Половина — не дольше 2 дн., 85% — не дольше 2 дн.")).toBeInTheDocument();
    expect(screen.getByText("За период сделано 2 задачи.")).toBeInTheDocument();
    expect(screen.getByRole("img", { name: "Задачи по статусам" })).toBeInTheDocument();
    // The legend names the bands with today's counts.
    const legend = screen.getByText("Готово").closest("li")!;
    expect(within(legend).getByText("2")).toBeInTheDocument();
    const old = screen.getByRole("link", { name: /Отчёт по сменам/ });
    expect(old).toHaveAttribute("href", expect.stringContaining("/tasks/t4"));
    expect(within(old).getByLabelText("Заблокирована")).toBeInTheDocument();
    expect(screen.getByText("21,1 дн.")).toBeInTheDocument();
    expect(queries[0]).toBe("?days=30");
    expect(screen.getByRole("link", { name: /Поток/ })).toHaveAttribute("aria-current", "page");
  });

  it("asks for another period", async () => {
    const user = userEvent.setup();
    const { api, queries } = server(flow);
    renderApp("/tasks?layout=flow", { api });
    await user.selectOptions(await screen.findByLabelText("Период", {}, firstLook), "90");
    expect(await screen.findByRole("heading", { level: 2, name: "Время цикла" })).toBeInTheDocument();
    expect(queries.at(-1)).toBe("?days=90");
  });

  it("says when the period has no tasks", async () => {
    const empty: TaskFlow = { ...flow, cycle: [], cycle_p50: null, cycle_p85: null, aging: [], cfd: flow.cfd.map((d) => ({ ...d, open: 0, in_progress: 0, done: 0 })) };
    renderApp("/tasks?layout=flow", { api: server(empty).api });
    expect(await screen.findByRole("heading", { name: "За период задач нет" }, firstLook)).toBeInTheDocument();
  });
});
