import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { installNotifier } from "../../lib/notifications/instance";
import { DEFAULT_NOTIFY_SETTINGS, readNotifySettings, useNotifySettings } from "../../lib/notifications/settings";
import { fakeApi, json, ownerSession } from "../../test/fake-api";
import { inboxOperator } from "../../test/inbox-fixtures";
import { renderApp } from "../../test/render-app";

class FakeNotification {
  static permission: NotificationPermission = "default";
  static requestPermission = vi.fn(() => {
    FakeNotification.permission = "granted";
    return Promise.resolve<NotificationPermission>("granted");
  });
}

describe("notification settings", () => {
  beforeEach(() => {
    FakeNotification.permission = "default";
    vi.stubGlobal("Notification", FakeNotification);
    window.localStorage.clear();
    useNotifySettings.setState(DEFAULT_NOTIFY_SETTINGS);
  });
  afterEach(() => {
    vi.unstubAllGlobals();
    installNotifier(undefined);
  });

  it("asks the browser for permission only on a click, then offers to turn desktop notifications off", async () => {
    const user = userEvent.setup();
    renderApp("/settings/notifications");
    expect(await screen.findByRole("heading", { level: 2, name: "Уведомления" })).toBeInTheDocument();
    expect(screen.getByText("Браузер ещё не спрашивал, можно ли показывать уведомления.")).toBeInTheDocument();
    expect(FakeNotification.requestPermission).not.toHaveBeenCalled();

    await user.click(screen.getByRole("button", { name: "Разрешить уведомления" }));
    expect(await screen.findByText("Разрешены в этом браузере.")).toBeInTheDocument();
    const desktop = screen.getByRole("checkbox", { name: "Показывать уведомления на рабочем столе" });
    expect(desktop).toBeChecked();
    await user.click(desktop);
    expect(readNotifySettings().desktop).toBe(false);
  });

  it("explains a blocked permission instead of offering a button that cannot work", async () => {
    FakeNotification.permission = "denied";
    renderApp("/settings/notifications");
    expect(await screen.findByText(/Заблокированы в браузере/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Разрешить уведомления" })).not.toBeInTheDocument();
  });

  it("keeps sounds and volume in this browser and sends a test notification", async () => {
    const user = userEvent.setup();
    const test = vi.fn();
    installNotifier({ notify: vi.fn(), notifyTask: vi.fn(), test });
    renderApp("/settings/notifications");
    const queue = await screen.findByRole("checkbox", { name: "Новый диалог в очереди" });
    await user.click(queue);
    expect(readNotifySettings().sounds).toEqual({ assigned: true, message: true, queued: false });
    expect(screen.getByRole("button", { name: "Прослушать: Мне назначили диалог" })).toBeInTheDocument();
    expect(screen.getByRole("slider", { name: "Громкость" })).toHaveValue("60");

    await user.click(screen.getByRole("button", { name: "Отправить тестовое уведомление" }));
    expect(test).toHaveBeenCalledTimes(1);
    expect(screen.getByText(/Отправлено\. Если ничего не появилось/)).toBeInTheDocument();
  });

  it("saves the email settings on the server", async () => {
    const user = userEvent.setup();
    let stored = { email_missed: true, email_delay_minutes: 15, email_available: true, telegram_assigned: true, telegram_messages: true, telegram_queue: true };
    const api = fakeApi({
      routes: {
        "GET /me/notifications": () => json(200, stored),
        "PATCH /me/notifications": async (request) => {
          stored = { ...stored, ...((await request.json()) as object) };
          return json(200, stored);
        },
      },
    });
    renderApp("/settings/notifications", { api });
    const missed = await screen.findByRole("checkbox", { name: "Письмо о непрочитанных сообщениях" });
    expect(missed).toHaveAccessibleDescription(expect.stringContaining(ownerSession.user.email) as string);
    await user.selectOptions(screen.getByLabelText("Через сколько"), "5");
    expect(await screen.findByText("Сохранено.")).toBeInTheDocument();
    await user.click(missed);
    await waitFor(() => expect(screen.getByLabelText("Через сколько")).toBeDisabled());
    const patches = api.calls.filter((c) => c.method === "PATCH" && c.path === "/me/notifications").map((c) => c.body);
    expect(patches).toEqual([{ email_delay_minutes: 5 }, { email_missed: false }]);
  });

  it("lets an operator with Telegram choose the messages about conversations", async () => {
    const user = userEvent.setup();
    let stored = { email_missed: true, email_delay_minutes: 15, email_available: true, telegram_assigned: true, telegram_messages: true, telegram_queue: true };
    const api = fakeApi({
      session: { ...inboxOperator, permissions: [...inboxOperator.permissions, "tasks.view"] },
      routes: {
        "GET /me/notifications": () => json(200, stored),
        "GET /me/telegram": () =>
          json(200, { bot_username: "zerno_alerts_bot", linked: true, name: "Пётр", linked_at: "2026-10-23T09:00:00Z", pending: false }),
        "PATCH /me/notifications": async (request) => {
          stored = { ...stored, ...((await request.json()) as object) };
          return json(200, stored);
        },
      },
    });
    renderApp("/settings/notifications", { api });
    const chats = await screen.findByRole("region", { name: "Диалоги в Telegram" });
    const queue = within(chats).getByRole("checkbox", { name: "Клиент ждёт в очереди" });
    expect(queue).toBeChecked();
    expect(within(chats).getByRole("checkbox", { name: "Клиент написал в мой диалог" })).toHaveAccessibleDescription(
      "Не чаще одного сообщения в 5 минут на диалог.",
    );
    await user.click(queue);
    await waitFor(() => expect(queue).not.toBeChecked());
    const patches = api.calls.filter((c) => c.method === "PATCH" && c.path === "/me/notifications").map((c) => c.body);
    expect(patches).toEqual([{ telegram_queue: false }]);
  });

  it("hides the messages about conversations without a linked Telegram", async () => {
    const api = fakeApi({
      session: inboxOperator,
      routes: {
        "GET /me/telegram": () => json(200, { bot_username: "zerno_alerts_bot", linked: false, name: null, linked_at: null, pending: false }),
      },
    });
    renderApp("/settings/notifications", { api });
    expect(await screen.findByRole("button", { name: "Привязать Telegram" })).toBeInTheDocument();
    expect(screen.queryByRole("region", { name: "Диалоги в Telegram" })).not.toBeInTheDocument();
  });

  it("says when the server cannot send email", async () => {
    const api = fakeApi({
      routes: {
        "GET /me/notifications": () => json(200, { email_missed: true, email_delay_minutes: 15, email_available: false, telegram_assigned: true, telegram_messages: true, telegram_queue: true }),
      },
    });
    renderApp("/settings/notifications", { api });
    expect(await screen.findByText(/На сервере не настроена отправка почты/)).toBeInTheDocument();
  });
});
