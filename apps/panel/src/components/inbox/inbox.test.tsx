import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import { applySignal, resetSignals } from "../../lib/realtime/signals";
import { fakeApi, json, operatorSession, problem, type FakeRoutes } from "../../test/fake-api";
import { conv, inboxOperator, msg } from "../../test/inbox-fixtures";
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
    expect(articles[0]).toHaveTextContent("видео.mp4файл не получен: больше лимита или недоступен в Telegram");
    expect(within(articles[0]!).queryByRole("button", { name: /Скачать/ })).not.toBeInTheDocument();
    expect(articles[1]).toHaveTextContent(/^Отправлено из Telegram · \d{2}:\d{2}Отправил на почту$/);
    expect(articles[2]).toHaveTextContent("Стикер — панель такие сообщения не показывает.");
  });

  it("shows an app report: app details, a log to download, a screenshot and why a file is missing", async () => {
    const user = userEvent.setup();
    const followed: string[] = [];
    const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (this: HTMLAnchorElement) {
      followed.push(this.getAttribute("href") ?? "");
    });
    const link = (id: string) => ({ method: "GET", url: `/api/v1/files/ws/w1/reports/${id}?exp=1&sig=s`, headers: {}, expires_at: "2026-10-05T12:15:00Z" });
    const api = inboxApi({
      "GET /conversations/c1/messages": () =>
        json(200, {
          items: [
            msg("r1", {
              body: "Не синхронизируется корзина",
              report: {
                report_id: "rep1",
                app: { name: "Зерно", version: "2.4.1", os: "Windows", os_version: "11 23H2", extra: { build: "1187" } },
                contact_verified: false,
                files: ["ok", "ok", "type_mismatch"],
              },
              attachments: [
                { id: "a-log", file_name: "app.log", content_type: "text/plain", size: 38912, status: "ready" },
                { id: "a-shot", file_name: "screen.png", content_type: "image/png", size: 204800, status: "ready" },
                { id: "a-fake", file_name: "fake.png", content_type: "image/png", size: 40, status: "rejected" },
              ],
            }),
          ],
          next_cursor: null,
        }),
      "GET /attachments/a-log/download-link": () => json(200, link("log")),
      "GET /attachments/a-shot/download-link": () => json(200, link("shot")),
    });
    renderApp("/inbox/c1", { api });
    const feed = await screen.findByRole("log", { name: "Сообщения диалога" });
    const report = (await within(feed).findAllByRole("article"))[0]!;
    expect(report).toHaveTextContent("Отчёт из приложенияЗерно 2.4.1Windows 11 23H2");
    expect(within(report).getByText("Личность не подтверждена")).toBeInTheDocument();

    // The log is a file to download, never shown as a page.
    expect(report).toHaveTextContent(/app\.log38\sкБСкачать/);
    await user.click(within(report).getByRole("button", { name: "Скачать app.log" }));
    expect(followed).toEqual(["/api/v1/files/ws/w1/reports/log?exp=1&sig=s"]);

    // The screenshot shows in the feed and opens larger.
    const shot = await within(report).findByRole("button", { name: "Открыть скриншот screen.png" });
    await waitFor(() => expect(shot.querySelector("img")).toHaveAttribute("src", "/api/v1/files/ws/w1/reports/shot?exp=1&sig=s"));
    await user.click(shot);
    const viewer = screen.getByRole("dialog", { name: "Скриншот screen.png" });
    expect(within(viewer).getByRole("img", { name: "screen.png" })).toBeInTheDocument();
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(shot).toHaveFocus();

    // The rejected file stays with the reason and no download.
    expect(report).toHaveTextContent("fake.pngотклонён: содержимое не совпадает с типом файла");
    expect(within(report).queryByRole("button", { name: "Скачать fake.png" })).not.toBeInTheDocument();
    expect(api.calls.some((c) => c.path.includes("a-fake"))).toBe(false);
    click.mockRestore();
  });

  it("shows how far replies got through Telegram and sends a failed one again", async () => {
    const user = userEvent.setup();
    const reply = (id: string, patch: Parameters<typeof msg>[1]) =>
      msg(id, { sender_type: "operator", sender_id: me, direction: "out", ...patch });
    const failed = reply("r3", { body: "Курьер выехал", delivery_status: "failed", delivery_error: "blocked", created_at: "2026-10-04T12:03:00Z" });
    const pending = { ...failed, delivery_status: "pending" as const, delivery_error: undefined };
    let retried = 0;
    const api = fakeApi({
      session: inboxOperator,
      routes: {
        "GET /conversations/c1": () => json(200, conv("c1", { channel_id: "ch1" })),
        "GET /conversations/c1/messages": () =>
          json(200, {
            items: [
              retried ? pending : failed,
              reply("r2", { body: "Заказ собран", delivery_status: "read", created_at: "2026-10-04T12:02:00Z" }),
              reply("r1", { body: "Проверяю", delivery_status: "pending", created_at: "2026-10-04T12:01:00Z" }),
              msg("n1", { kind: "note", is_private: true, sender_type: "operator", sender_id: me, direction: "out", body: "VIP", created_at: "2026-10-04T12:00:00Z" }),
            ],
            next_cursor: null,
          }),
        "POST /conversations/c1/messages/r3/retry": () => {
          retried++;
          return json(200, pending);
        },
      },
    });
    renderApp("/inbox/c1", { api });
    const feed = await screen.findByRole("log", { name: "Сообщения диалога" });
    const articles = await within(feed).findAllByRole("article");
    expect(articles[0]).not.toHaveTextContent(/отправ|прочитано/); // a note goes nowhere
    expect(articles[1]).toHaveTextContent(/Проверяюотправляется$/);
    expect(articles[2]).toHaveTextContent(/Заказ собранпрочитано$/);
    expect(articles[3]).toHaveTextContent("Не доставлено: клиент заблокировал бота или аккаунт");

    await user.click(within(articles[3]!).getByRole("button", { name: "Повторить" }));
    expect(retried).toBe(1);
    expect(await within(feed).findAllByText("отправляется")).toHaveLength(2);
    expect(within(feed).queryByText(/Не доставлено/)).not.toBeInTheDocument();
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

  it("shows the customer's rating of the resolved conversation", async () => {
    const rated = conv("c1", {
      status: "resolved",
      rating: { score: 4, comment: "Быстро помогли", resolved_by: "human", rated_at: "2026-10-06T10:00:00Z" },
    });
    renderApp("/inbox/c1", { api: inboxApi({ "GET /conversations/c1": () => json(200, rated) }) });
    const feed = await screen.findByRole("log", { name: "Сообщения диалога" });
    expect(await within(feed).findByText("Клиент оценил диалог: 4 из 5")).toBeInTheDocument();
    expect(within(feed).getByLabelText("Комментарий клиента")).toHaveTextContent("Быстро помогли");
    expect(screen.getByText("Оценка 4 из 5")).toBeInTheDocument();
  });

  it("says when a conversation does not exist", async () => {
    renderApp("/inbox/c9", { api: inboxApi({ "GET /conversations/c9": () => problem(404, "not_found") }) });
    expect(await screen.findByText("Диалог не найден или недоступен")).toBeInTheDocument();
  });
});
