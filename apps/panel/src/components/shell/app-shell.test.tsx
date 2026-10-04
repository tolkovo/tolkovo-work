import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { renderApp } from "../../test/render-app";

describe("panel shell", () => {
  it("redirects / to the inbox", async () => {
    const { router } = renderApp("/");
    expect(await screen.findByRole("heading", { level: 1, name: "Диалоги" })).toBeInTheDocument();
    expect(router.state.location.pathname).toBe("/inbox");
  });

  it("shows the five sections in the rail and marks the current one", async () => {
    renderApp("/monitor");
    const nav = await screen.findByRole("navigation", { name: "Разделы" });
    const links = within(nav).getAllByRole("link");
    expect(links.map((l) => l.textContent)).toEqual(["Диалоги", "Монитор", "Аналитика", "База", "Настройки"]);
    expect(within(nav).getByRole("link", { name: "Монитор" })).toHaveAttribute("aria-current", "page");
    expect(within(nav).getByRole("link", { name: "Диалоги" })).not.toHaveAttribute("aria-current");
  });

  it.each([
    ["/inbox", "Диалогов пока нет", "Подключить канал", "/settings"],
    ["/monitor", "Сейчас никого нет на смене", "Пригласить операторов", "/settings"],
    ["/analytics", "Данных пока нет", "Открыть диалоги", "/inbox"],
    ["/kb", "Статей пока нет", "Настроить ИИ", "/settings"],
    ["/settings", "Настройки откроются после входа", "Войти", "/login"],
  ])("%s shows an empty state with a call to action", async (url, title, action, href) => {
    renderApp(url);
    expect(await screen.findByRole("heading", { level: 2, name: title })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: action })).toHaveAttribute("href", href);
  });

  it("navigates between sections from the rail", async () => {
    const user = userEvent.setup();
    const { router } = renderApp("/inbox");
    await user.click(await screen.findByRole("link", { name: "Аналитика" }));
    expect(await screen.findByRole("heading", { level: 1, name: "Аналитика" })).toBeInTheDocument();
    expect(router.state.location.pathname).toBe("/analytics");
  });

  it("shows the 404 page for an unknown address", async () => {
    renderApp("/no-such-page");
    expect(await screen.findByRole("heading", { name: "Страница не найдена" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Перейти к диалогам" })).toHaveAttribute("href", "/inbox");
  });

  it("has a skip link to the main content", async () => {
    renderApp("/inbox");
    const skip = await screen.findByRole("link", { name: "Перейти к содержимому" });
    expect(skip).toHaveAttribute("href", "#main");
    expect(document.getElementById("main")).toBeInTheDocument();
  });
});

describe("theme switcher", () => {
  it("applies and remembers the chosen theme, and returns to the system theme", async () => {
    const user = userEvent.setup();
    renderApp("/inbox");
    const group = await screen.findByRole("group", { name: "Тема" });

    await user.click(within(group).getByRole("button", { name: "Тёмная" }));
    expect(document.documentElement).toHaveAttribute("data-theme", "dark");
    expect(window.localStorage.getItem("tolkovo.theme")).toBe("dark");
    expect(within(group).getByRole("button", { name: "Тёмная" })).toHaveAttribute("aria-pressed", "true");

    await user.click(within(group).getByRole("button", { name: "Как в системе" }));
    expect(document.documentElement).not.toHaveAttribute("data-theme");
    expect(window.localStorage.getItem("tolkovo.theme")).toBeNull();
  });
});

describe("language switcher", () => {
  it("switches the interface to English and remembers it", async () => {
    const user = userEvent.setup();
    renderApp("/inbox");
    const group = await screen.findByRole("group", { name: "Язык интерфейса" });

    await user.click(within(group).getByRole("button", { name: "EN" }));

    expect(await screen.findByRole("heading", { level: 1, name: "Inbox" })).toBeInTheDocument();
    expect(screen.getByRole("navigation", { name: "Sections" })).toBeInTheDocument();
    expect(document.documentElement.lang).toBe("en");
    expect(window.localStorage.getItem("tolkovo.lang")).toBe("en");
    await waitFor(() =>
      expect(screen.getByRole("group", { name: "Interface language" })).toBeInTheDocument(),
    );
  });
});
