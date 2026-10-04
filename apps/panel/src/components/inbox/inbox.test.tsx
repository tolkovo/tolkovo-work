import { act, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it } from "vitest";

import { applySignal, resetSignals } from "../../lib/realtime/signals";
import { fakeApi, json, operatorSession, problem, type FakeRoutes } from "../../test/fake-api";
import { conv, msg } from "../../test/inbox-fixtures";
import { renderApp } from "../../test/render-app";

const me = operatorSession.user.id;

function inboxApi(extra: FakeRoutes = {}) {
  const queued = conv("c2", {
    number: 8,
    status: "queued",
    assignee_id: null,
    contact: { id: "k-c2", name: "Гость 4821" },
    last_message: { sender_type: "contact", kind: "media", text: "", attachments: 2, created_at: "2026-10-04T12:00:00Z" },
  });
  return fakeApi({
    session: operatorSession,
    routes: {
      "GET /conversations/counts": () => json(200, { mine: 1, mine_unread: 0, unassigned: 1, open: 2 }),
      "GET /conversations": (request) => {
        const q = new URL(request.url).searchParams;
        const items = q.get("assignee") === "me" ? [conv("c1")] : q.get("assignee") === "none" ? [queued] : [conv("c1"), queued];
        return json(200, { items, next_cursor: null });
      },
      "GET /conversations/c1": () => json(200, conv("c1")),
      "GET /conversations/c1/messages": () =>
        json(200, {
          // Newest first, as the server pages them.
          items: [
            msg("m3", { kind: "note", is_private: true, sender_type: "operator", sender_id: "u-anna", body: "Проверь в CRM", direction: "out", created_at: "2026-10-04T12:03:00Z" }),
            msg("m2", { sender_type: "operator", sender_id: me, direction: "out", body: "Смотрю заказ", created_at: "2026-10-04T12:02:00Z" }),
            msg("m1", { body: "С карты списали два раза" }),
          ],
          next_cursor: null,
        }),
      ...extra,
    },
  });
}

describe("inbox", () => {
  afterEach(() => resetSignals());

  it("shows the tabs with live counters and the operator's conversations", async () => {
    renderApp("/inbox", { api: inboxApi() });
    const tabs = await screen.findByRole("navigation", { name: "Папки диалогов" });
    expect(await within(tabs).findByRole("link", { name: "Мои 1" })).toHaveAttribute("aria-current", "page");
    expect(await within(tabs).findByRole("link", { name: "Очередь 1" })).toBeInTheDocument();

    const row = await screen.findByRole("link", { name: /Марина Ковалёва/ });
    expect(row).toHaveTextContent("Тут есть кто-нибудь?");
    expect(within(row).getByText("Ваш ход")).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Выберите диалог" })).toBeInTheDocument();
  });

  it("switches tabs through the address and shows files when a message has no text", async () => {
    const user = userEvent.setup();
    const { router } = renderApp("/inbox", { api: inboxApi() });
    await user.click(await screen.findByRole("link", { name: /^Очередь/ }));
    expect(router.state.location.search).toEqual({ tab: "queue" });
    const row = await screen.findByRole("link", { name: /Гость 4821/ });
    expect(row).toHaveTextContent("2 файла");
  });

  it("shows what came through Telegram: owner replies, stickers and files that did not arrive", async () => {
    const api = inboxApi({
      "GET /conversations/c1/messages": () =>
        json(200, {
          items: [
            msg("t3", { body: "", kind: "text", unsupported: "sticker", created_at: "2026-10-04T12:03:00Z" }),
            msg("t2", {
              sender_type: "operator", sender_id: null, direction: "out", sent_outside_panel: true,
              body: "Отправил на почту", delivery_status: "sent", created_at: "2026-10-04T12:02:00Z",
            }),
            msg("t1", {
              body: "", kind: "media",
              attachments: [{ id: "a1", file_name: "видео.mp4", content_type: "video/mp4", size: 31457280, status: "rejected" }],
            }),
          ],
          next_cursor: null,
        }),
    });
    renderApp("/inbox/c1", { api });
    const feed = await screen.findByRole("log", { name: "Сообщения диалога" });
    const articles = await within(feed).findAllByRole("article");
    expect(articles[0]).toHaveTextContent("видео.mp4 — файл не получен: больше лимита или недоступен в Telegram");
    expect(articles[1]).toHaveTextContent(/^Отправлено из Telegram · \d{2}:\d{2}Отправил на почту$/);
    expect(articles[2]).toHaveTextContent("Стикер — панель такие сообщения не показывает.");
  });

  it("opens a conversation with its feed in reading order and marks internal notes", async () => {
    const user = userEvent.setup();
    const { router } = renderApp("/inbox", { api: inboxApi() });
    await user.click(await screen.findByRole("link", { name: /Марина Ковалёва/ }));
    expect(router.state.location.pathname).toBe("/inbox/c1");
    expect(await screen.findByRole("heading", { level: 2, name: /Марина Ковалёва\s*№/ })).toBeInTheDocument();
    expect(screen.getByText("Ведёт: Пётр Иванов (вы)")).toBeInTheDocument();

    const feed = screen.getByRole("log", { name: "Сообщения диалога" });
    const bodies = (await within(feed).findAllByRole("article")).map((a) => a.textContent);
    expect(bodies[0]).toContain("С карты списали два раза");
    expect(bodies[1]).toContain("Смотрю заказ");
    expect(bodies[2]).toContain("Заметка · клиент не видит · Анна Смирнова");
  });

  it("shows who else has the conversation open and who types", async () => {
    renderApp("/inbox/c1", { api: inboxApi() });
    await screen.findByRole("log", { name: "Сообщения диалога" });
    act(() =>
      applySignal({
        type: "conversation.viewers",
        id: "v",
        ts: "2026-10-04T12:00:00Z",
        data: { conversation_id: "c1", user_ids: [me, "u-anna"] },
      }),
    );
    expect(await screen.findByText("Анна Смирнова смотрит диалог")).toBeInTheDocument();
    act(() =>
      applySignal({
        type: "conversation.typing",
        id: "t",
        ts: "2026-10-04T12:00:00Z",
        data: { conversation_id: "c1", actor: "operator", actor_id: "u-anna", state: "start" },
      }),
    );
    expect(await screen.findByText("Анна Смирнова печатает…")).toBeInTheDocument();
    expect(screen.queryByText("Анна Смирнова смотрит диалог")).not.toBeInTheDocument();
    // The row in the list shows the colleague too, with the state in its label.
    expect(screen.getByRole("img", { name: "Анна Смирнова печатает…" })).toBeInTheDocument();
  });

  it("says when a conversation does not exist", async () => {
    renderApp("/inbox/c9", { api: inboxApi({ "GET /conversations/c9": () => problem(404, "not_found") }) });
    expect(await screen.findByText("Диалог не найден или недоступен")).toBeInTheDocument();
  });
});
