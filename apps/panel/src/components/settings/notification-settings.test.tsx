import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { installNotifier } from "../../lib/notifications/instance";
import { DEFAULT_NOTIFY_SETTINGS, readNotifySettings, useNotifySettings } from "../../lib/notifications/settings";
import { fakeApi, json, ownerSession } from "../../test/fake-api";
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
    let stored = { email_missed: true, email_delay_minutes: 15, email_available: true };
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

  it("says when the server cannot send email", async () => {
    const api = fakeApi({
      routes: {
        "GET /me/notifications": () => json(200, { email_missed: true, email_delay_minutes: 15, email_available: false }),
      },
    });
    renderApp("/settings/notifications", { api });
    expect(await screen.findByText(/На сервере не настроена отправка почты/)).toBeInTheDocument();
  });
});
