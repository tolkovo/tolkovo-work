import type { Session } from "@tolkovo/api-client";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { deviceSessions, fakeApi, json, operatorSession, ownerSession, problem } from "../../test/fake-api";
import { renderApp } from "../../test/render-app";

function settingsNav() {
  return screen.findByRole("navigation", { name: "Разделы настроек" });
}

function navLinks(nav: HTMLElement) {
  return within(nav)
    .getAllByRole("link")
    .map((l) => l.textContent);
}

describe("settings tree", () => {
  it("gives an operator the personal sections and the team, not the workspace settings", async () => {
    const { router } = renderApp("/settings", { api: fakeApi({ session: operatorSession }) });
    expect(await screen.findByRole("heading", { level: 2, name: "Профиль" })).toBeInTheDocument();
    expect(router.state.location.pathname).toBe("/settings/profile");
    const nav = await settingsNav();
    expect(navLinks(nav)).toEqual(["Команда", "Профиль", "Внешний вид", "Безопасность и вход"]);
    expect(within(nav).getByRole("heading", { name: "Пространство" })).toBeInTheDocument();
    expect(within(nav).getByRole("heading", { name: "Лично для меня" })).toBeInTheDocument();
  });

  it("drops the workspace group for a role without workspace permissions", async () => {
    const reader: Session = { ...operatorSession, permissions: [] };
    renderApp("/settings", { api: fakeApi({ session: reader }) });
    const nav = await settingsNav();
    expect(navLinks(nav)).toEqual(["Профиль", "Внешний вид", "Безопасность и вход"]);
    expect(within(nav).queryByRole("heading", { name: "Пространство" })).not.toBeInTheDocument();
  });

  it("gives the owner both groups and opens General first", async () => {
    const { router } = renderApp("/settings");
    const nav = await settingsNav();
    expect(navLinks(nav)).toEqual(["Общие", "Команда", "Профиль", "Внешний вид", "Безопасность и вход"]);
    await waitFor(() => expect(router.state.location.pathname).toBe("/settings/general"));
  });
});

describe("profile", () => {
  it("shows the email read-only and saves name and language to the server", async () => {
    const user = userEvent.setup();
    const api = fakeApi();
    renderApp("/settings/profile", { api });
    const name = await screen.findByLabelText("Имя");
    expect(screen.getByLabelText("Эл. почта")).toHaveValue("anna@zerno.test");
    expect(screen.getByLabelText("Эл. почта")).toHaveAttribute("readonly");

    await user.clear(name);
    await user.type(name, "Анна Петрова");
    await user.selectOptions(screen.getByLabelText("Язык интерфейса", { selector: "select" }), "en");
    await user.click(screen.getByRole("button", { name: "Сохранить" }));

    // The interface follows the saved language, and the top bar shows the new name.
    expect(await screen.findByRole("heading", { level: 2, name: "Profile" })).toBeInTheDocument();
    expect(screen.getByText("Saved")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Анна Петрова" })).toHaveAttribute("href", "/settings/profile");
    const call = api.calls.find((c) => c.method === "PATCH" && c.path === "/me");
    expect(call?.body).toEqual({ name: "Анна Петрова", locale: "en" });
    expect(call?.headers.get("X-CSRF-Token")).toBe("csrf-owner");
    expect(window.localStorage.getItem("tolkovo.lang")).toBe("en"); // the sign-in screen fallback
  });

  it("does not send an empty name", async () => {
    const user = userEvent.setup();
    const api = fakeApi();
    renderApp("/settings/profile", { api });
    const name = await screen.findByLabelText("Имя");
    await user.clear(name);
    await user.click(screen.getByRole("button", { name: "Сохранить" }));
    expect(name).toHaveAccessibleDescription(expect.stringContaining("Заполните поле.") as string);
    expect(name).toHaveFocus();
    expect(api.calls.some((c) => c.method === "PATCH")).toBe(false);
  });

  it("is open to an operator", async () => {
    renderApp("/settings/profile", { api: fakeApi({ session: operatorSession }) });
    expect(await screen.findByLabelText("Имя")).toHaveValue("Пётр Иванов");
    expect(screen.getByRole("button", { name: "Сохранить" })).toBeEnabled();
  });
});

describe("appearance", () => {
  it("switches the theme for this browser only and says so", async () => {
    const user = userEvent.setup();
    renderApp("/settings/appearance");
    const dark = await screen.findByRole("radio", { name: "Тёмная" });
    const main = screen.getByRole("main");
    expect(within(main).getByRole("radio", { name: "Как в системе" })).toBeChecked();
    expect(within(main).getByRole("group", { name: "Тема" })).toHaveAccessibleDescription(
      "Тема хранится только в этом браузере: на другом устройстве выберите её отдельно.",
    );
    await user.click(dark);
    expect(dark).toBeChecked();
    expect(document.documentElement).toHaveAttribute("data-theme", "dark");
    expect(window.localStorage.getItem("tolkovo.theme")).toBe("dark");
  });
});

describe("password change", () => {
  const fill = async (user: ReturnType<typeof userEvent.setup>, current: string, next: string, confirm = next) => {
    await user.type(await screen.findByLabelText("Текущий пароль"), current);
    await user.type(screen.getByLabelText("Новый пароль"), next);
    await user.type(screen.getByLabelText("Повторите новый пароль"), confirm);
    await user.click(screen.getByRole("button", { name: "Сменить пароль" }));
  };

  it("catches a mistyped confirmation before sending", async () => {
    const user = userEvent.setup();
    const api = fakeApi();
    renderApp("/settings/security", { api });
    await fill(user, "old password", "new password", "new pasword");
    expect(screen.getByLabelText("Повторите новый пароль")).toHaveAccessibleDescription("Пароли не совпадают.");
    expect(api.calls.some((c) => c.path === "/me/password")).toBe(false);
  });

  it("marks a wrong current password at its field", async () => {
    const user = userEvent.setup();
    renderApp("/settings/security", { api: fakeApi({ routes: { "POST /me/password": () => problem(422, "wrong_password") } }) });
    await fill(user, "wrong password", "new password");
    const current = screen.getByLabelText("Текущий пароль");
    await waitFor(() => expect(current).toHaveAccessibleDescription("Текущий пароль указан неверно."));
    await waitFor(() => expect(current).toHaveFocus());
    expect(current).toHaveAttribute("aria-invalid", "true");
  });

  it("explains the rate limit", async () => {
    const user = userEvent.setup();
    renderApp("/settings/security", {
      api: fakeApi({ routes: { "POST /me/password": () => problem(429, "too_many_requests", { "Retry-After": "120" }) } }),
    });
    await fill(user, "old password", "new password");
    const form = screen.getByRole("form", { name: "Смена пароля" });
    expect(await within(form).findByRole("alert")).toHaveTextContent("Слишком много попыток. Повторите через 120 секунд.");
  });

  it("changes the password and keeps working with the rotated session", async () => {
    const user = userEvent.setup();
    const rotated = { ...ownerSession, csrf_token: "csrf-rotated" };
    const api = fakeApi({
      routes: {
        "POST /me/password": () => json(200, rotated),
        "POST /me/sessions/revoke-others": () => new Response(null, { status: 204 }),
      },
    });
    renderApp("/settings/security", { api });
    await fill(user, "old password", "new password");

    expect(await screen.findByText("Пароль изменён. На других устройствах выполнен выход.")).toBeInTheDocument();
    const call = api.calls.find((c) => c.path === "/me/password");
    expect(call?.body).toEqual({ current_password: "old password", new_password: "new password" });
    expect(call?.headers.get("X-CSRF-Token")).toBe("csrf-owner");
    expect(screen.getByLabelText("Текущий пароль")).toHaveValue("");
    // The session list is reloaded, and the next unsafe request carries the new CSRF token.
    await waitFor(() => expect(api.calls.filter((c) => c.path === "/me/sessions")).toHaveLength(2));
    await user.click(screen.getByRole("button", { name: "Выйти на других устройствах" }));
    await waitFor(() =>
      expect(api.calls.find((c) => c.path === "/me/sessions/revoke-others")?.headers.get("X-CSRF-Token")).toBe(
        "csrf-rotated",
      ),
    );
  });
});

describe("active sessions", () => {
  it("lists devices with this one marked and ends another one", async () => {
    const user = userEvent.setup();
    let items = deviceSessions;
    const api = fakeApi({
      routes: {
        "GET /me/sessions": () => json(200, { items }),
        "DELETE /me/sessions/s-phone": () => {
          items = items.filter((s) => s.id !== "s-phone");
          return new Response(null, { status: 204 });
        },
      },
    });
    renderApp("/settings/security", { api });
    const list = await screen.findByRole("list", { name: "Активные сеансы" });
    const rows = within(list).getAllByRole("listitem");
    expect(rows).toHaveLength(2);
    expect(rows[0]).toHaveTextContent("Firefox · macOS");
    expect(rows[0]).toHaveTextContent("Это устройство");
    expect(within(rows[0]!).queryByRole("button")).not.toBeInTheDocument();
    expect(rows[1]).toHaveTextContent("Safari · iOS");

    await user.click(within(rows[1]!).getByRole("button", { name: "Завершить сеанс: Safari · iOS" }));

    expect(await screen.findByText("Сеанс завершён.")).toBeInTheDocument();
    await waitFor(() => expect(within(list).getAllByRole("listitem")).toHaveLength(1));
    expect(screen.getByText("Других активных сеансов нет.")).toBeInTheDocument();
    expect(api.calls.find((c) => c.method === "DELETE")?.headers.get("X-CSRF-Token")).toBe("csrf-owner");
  });

  it("says when a session has already ended elsewhere", async () => {
    const user = userEvent.setup();
    renderApp("/settings/security", {
      api: fakeApi({ routes: { "DELETE /me/sessions/s-phone": () => problem(404, "not_found") } }),
    });
    await user.click(await screen.findByRole("button", { name: "Завершить сеанс: Safari · iOS" }));
    expect(await screen.findByText("Этот сеанс уже завершён.")).toBeInTheDocument();
  });

  it("signs out on every other device", async () => {
    const user = userEvent.setup();
    const api = fakeApi({ routes: { "POST /me/sessions/revoke-others": () => new Response(null, { status: 204 }) } });
    renderApp("/settings/security", { api });
    await user.click(await screen.findByRole("button", { name: "Выйти на других устройствах" }));
    expect(await screen.findByText("Выполнен выход на других устройствах.")).toBeInTheDocument();
    expect(api.calls.some((c) => c.method === "POST" && c.path === "/me/sessions/revoke-others")).toBe(true);
  });

  it("offers a retry when the list fails to load", async () => {
    const user = userEvent.setup();
    let fail = true;
    renderApp("/settings/security", {
      api: fakeApi({
        routes: { "GET /me/sessions": () => (fail ? problem(403, "forbidden") : json(200, { items: deviceSessions })) },
      }),
    });
    const section = await screen.findByRole("region", { name: "Активные сеансы" });
    expect(await within(section).findByText("Не хватает прав. Обратитесь к владельцу пространства.")).toBeInTheDocument();
    fail = false;
    await user.click(within(section).getByRole("button", { name: "Повторить" }));
    expect(await within(section).findByText("Firefox · macOS")).toBeInTheDocument();
  });
});
