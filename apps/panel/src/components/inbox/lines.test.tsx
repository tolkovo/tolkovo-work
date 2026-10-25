import type { Conversation, Line } from "@tolkovo/api-client";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { fakeApi, json, ownerSession } from "../../test/fake-api";
import { conv, inboxOperator, msg } from "../../test/inbox-fixtures";
import { renderApp } from "../../test/render-app";

/** Lines of support L1/L2/L3 in the inbox and the settings (ADR-0021). */

const BILLING = "0199a000-0000-7000-8000-0000000011b2";
const SUPPORT = "0199a000-0000-7000-8000-0000000011b1";

const lines: Line[] = [
  { id: SUPPORT, name: "Поддержка", level: 1, member_ids: [] },
  { id: BILLING, name: "Биллинг", level: 2, member_ids: ["u-anna"] },
];

describe("lines in the inbox", () => {
  it("passes the conversation up with a reason and names the line in the feed", async () => {
    const user = userEvent.setup();
    let conversation: Conversation = conv("c1");
    const api = fakeApi({
      session: inboxOperator,
      routes: {
        "GET /lines": () => json(200, { items: lines }),
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
                  type: "line_changed",
                  from: "assigned",
                  to: "queued",
                  assignee_id: null,
                  previous_assignee_id: null,
                  line_id: null,
                  previous_line_id: BILLING,
                },
              }),
              msg("m1", { body: "Списали дважды" }),
            ],
            next_cursor: null,
          }),
        "POST /conversations/c1/escalate": async (r) => {
          const body = (await r.json()) as { line_id: string | null };
          conversation = { ...conversation, line_id: body.line_id, line_level: 2, status: "queued", assignee_id: null, version: 2 };
          return json(200, conversation);
        },
        "GET /contacts/k-c1": () => json(404, {}),
      },
    });
    renderApp("/inbox/c1", { api });
    expect(await screen.findByText(/Возвращён на первую линию/)).toBeInTheDocument();

    await user.click(await screen.findByRole("button", { name: "Передать на линию" }));
    const form = screen.getByRole("form", { name: "Передать на линию" });
    // The first line above this one is offered first; up needs a reason.
    expect(within(form).getByLabelText("Куда")).toHaveValue(BILLING);
    await user.click(within(form).getByRole("button", { name: "Передать" }));
    expect(await within(form).findByText("Напишите причину: специалист увидит её первой.")).toBeInTheDocument();
    expect(api.calls.some((c) => c.path.endsWith("/escalate"))).toBe(false);

    await user.type(within(form).getByLabelText(/Причина/), "Двойное списание");
    await user.click(within(form).getByRole("button", { name: "Передать" }));
    await waitFor(() =>
      expect(api.calls.find((c) => c.path.endsWith("/escalate"))?.body).toEqual({ line_id: BILLING, reason: "Двойное списание", version: 1 }),
    );
    expect(await screen.findAllByText("Биллинг")).not.toHaveLength(0);
  });

  it("shows the line on rows and filters the list by it", async () => {
    const user = userEvent.setup();
    const up = conv("c1", { line_id: BILLING, line_level: 2, status: "queued", assignee_id: null });
    const first = conv("c2", { contact: { id: "k-c2", name: "Олег Петров" } });
    const api = fakeApi({
      session: inboxOperator,
      routes: {
        "GET /lines": () => json(200, { items: lines }),
        "GET /conversations": (r) => {
          const line = new URL(r.url).searchParams.get("line");
          const items = line === null ? [up, first] : line === "first" ? [first] : [up, first].filter((c) => c.line_id === line);
          return json(200, { items, next_cursor: null });
        },
      },
    });
    renderApp("/inbox?tab=all", { api });
    const row = (await screen.findByText("Марина Ковалёва")).closest("a")!;
    expect(within(row).getByText("L2")).toBeInTheDocument();

    const filter = screen.getByRole("group", { name: "Линии" });
    await user.click(within(filter).getByRole("button", { name: /Биллинг/ }));
    await waitFor(() => expect(screen.queryByText("Олег Петров")).not.toBeInTheDocument());
    expect(screen.getByRole("link", { name: /Очередь/ })).toHaveAttribute("href", expect.stringContaining(`line=${BILLING}`));
    await user.click(within(filter).getByRole("button", { name: "Первая линия" }));
    expect(await screen.findByText("Олег Петров")).toBeInTheDocument();
  });
});

describe("lines in the settings", () => {
  const admin = { ...ownerSession, permissions: [...ownerSession.permissions, "routing.manage"] };

  it("adds a line with its level and members", async () => {
    const user = userEvent.setup();
    const api = fakeApi({
      session: admin,
      routes: {
        "POST /lines": async (r) => json(201, { id: BILLING, ...((await r.json()) as object) }),
      },
    });
    renderApp("/settings/routing", { api });
    expect(await screen.findByText("Линий пока нет: все диалоги на первой линии.")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Добавить линию" }));
    await user.click(screen.getByRole("button", { name: "Сохранить" }));
    expect(await screen.findByText("Укажите название линии.")).toBeInTheDocument();
    await user.type(screen.getByLabelText("Название"), "Биллинг");
    await user.selectOptions(screen.getByLabelText("Уровень"), "3");
    await user.click(screen.getByRole("checkbox", { name: "Анна Смирнова" }));
    await user.click(screen.getByRole("button", { name: "Сохранить" }));
    await waitFor(() =>
      expect(api.calls.find((c) => c.method === "POST")?.body).toEqual({ name: "Биллинг", level: 3, member_ids: ["u-anna"] }),
    );
  });
});
