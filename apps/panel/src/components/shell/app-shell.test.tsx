import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { fakeApi, json, operatorSession, ownerSession, problem, workspaces } from "../../test/fake-api";
import { renderApp } from "../../test/render-app";

describe("panel shell", () => {
  it("redirects / to the inbox", async () => {
    const { router } = renderApp("/");
    expect(await screen.findByRole("heading", { level: 1, name: "Диалоги" })).toBeInTheDocument();
    expect(router.state.location.pathname).toBe("/inbox");
  });

  it("shows the sections in the rail and marks the current one", async () => {
    renderApp("/monitor");
    const nav = await screen.findByRole("navigation", { name: "Разделы" });
    const links = within(nav).getAllByRole("link");
    expect(links.map((l) => l.textContent)).toEqual(["Диалоги", "Монитор", "Аналитика", "Настройки"]);
    expect(within(nav).getByRole("link", { name: "Монитор" })).toHaveAttribute("aria-current", "page");
    expect(within(nav).getByRole("link", { name: "Диалоги" })).not.toHaveAttribute("aria-current");
  });

  it.each([
    ["/inbox", "Диалогов пока нет", "Подключить канал", "/settings"],
    ["/monitor", "Сейчас никого нет на смене", "Пригласить операторов", "/settings"],
    ["/analytics", "Данных пока нет", "Открыть диалоги", "/inbox"],
  ])("%s shows an empty state with a call to action", async (url, title, action, href) => {
    renderApp(url);
    expect(await screen.findByRole("heading", { level: 2, name: title })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: action })).toHaveAttribute("href", href);
  });

  it("leads those who keep the knowledge base to «Настройки → База знаний»", async () => {
    const session = { ...ownerSession, permissions: [...ownerSession.permissions, "knowledge.manage"] };
    const { router } = renderApp("/kb", { api: fakeApi({ session }) });
    const nav = await screen.findByRole("navigation", { name: "Разделы" });
    expect(within(nav).getByRole("link", { name: "База" })).toHaveAttribute("href", "/kb");
    await waitFor(() => expect(router.state.location.pathname).toBe("/settings/knowledge"));
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

describe("session in the shell", () => {
  it("shows settings to every role: personal settings belong to everyone", async () => {
    renderApp("/inbox", { api: fakeApi({ session: operatorSession }) });
    const nav = await screen.findByRole("navigation", { name: "Разделы" });
    expect(within(nav).getByRole("link", { name: "Настройки" })).toHaveAttribute("href", "/settings");
  });

  it("links the user's name to the profile", async () => {
    const user = userEvent.setup();
    const { router } = renderApp("/inbox");
    await user.click(await screen.findByRole("link", { name: "Анна Смирнова" }));
    expect(await screen.findByRole("heading", { level: 2, name: "Профиль" })).toBeInTheDocument();
    expect(router.state.location.pathname).toBe("/settings/profile");
  });

  it("applies the profile language over the one stored in the browser", async () => {
    // The browser remembers English (e.g. the sign-in screen), the profile says Russian.
    renderApp("/inbox", { language: "en" });
    expect(await screen.findByRole("heading", { level: 1, name: "Диалоги" })).toBeInTheDocument();
    expect(document.documentElement.lang).toBe("ru");
  });

  it("shows the workspace name and no switcher with one workspace", async () => {
    renderApp("/inbox");
    expect(await screen.findByText("Зерно")).toBeInTheDocument();
    expect(screen.queryByLabelText("Пространство")).not.toBeInTheDocument();
  });

  it("switches the workspace and refetches workspace data", async () => {
    const user = userEvent.setup();
    const other = { id: "w-other", slug: "other", name: "Другое", role: "admin" as const };
    const switched = { ...ownerSession, workspace: { ...ownerSession.workspace, ...other }, role: "admin" as const, csrf_token: "csrf-2" };
    const api = fakeApi({
      routes: {
        "GET /me/workspaces": () => json(200, { items: [...workspaces, other] }),
        "POST /auth/workspace": () => json(200, switched),
      },
    });
    const { router } = renderApp("/settings/team", { api });
    const select = await screen.findByLabelText("Пространство");
    await screen.findByRole("table");

    await user.selectOptions(select, "w-other");

    await waitFor(() => expect(router.state.location.pathname).toBe("/inbox"));
    expect(screen.getByLabelText("Пространство")).toHaveValue("w-other");
    const call = api.calls.find((c) => c.path === "/auth/workspace");
    expect(call?.body).toEqual({ workspace_id: "w-other" });
    expect(call?.headers.get("X-CSRF-Token")).toBe("csrf-owner");
  });

  it("signs out and returns to sign-in", async () => {
    const user = userEvent.setup();
    const api = fakeApi();
    const { router } = renderApp("/inbox", { api });
    await user.click(await screen.findByRole("button", { name: "Выйти" }));
    expect(await screen.findByRole("heading", { level: 1, name: "Вход" })).toBeInTheDocument();
    expect(router.state.location.pathname).toBe("/login");
    expect(api.calls.find((c) => c.path === "/auth/logout")?.headers.get("X-CSRF-Token")).toBe("csrf-owner");
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
  it("switches the interface to English and saves it to the profile", async () => {
    const user = userEvent.setup();
    const api = fakeApi();
    renderApp("/inbox", { api });
    const group = await screen.findByRole("group", { name: "Язык интерфейса" });

    await user.click(within(group).getByRole("button", { name: "EN" }));

    expect(await screen.findByRole("heading", { level: 1, name: "Inbox" })).toBeInTheDocument();
    expect(screen.getByRole("navigation", { name: "Sections" })).toBeInTheDocument();
    expect(document.documentElement.lang).toBe("en");
    expect(window.localStorage.getItem("tolkovo.lang")).toBe("en");
    await waitFor(() =>
      expect(screen.getByRole("group", { name: "Interface language" })).toBeInTheDocument(),
    );
    const call = api.calls.find((c) => c.method === "PATCH" && c.path === "/me");
    expect(call?.body).toEqual({ locale: "en" });
    expect(call?.headers.get("X-CSRF-Token")).toBe("csrf-owner");
  });

  it("returns to the profile language when saving fails", async () => {
    const user = userEvent.setup();
    renderApp("/inbox", { api: fakeApi({ routes: { "PATCH /me": () => problem(500, "internal") } }) });
    const group = await screen.findByRole("group", { name: "Язык интерфейса" });
    await user.click(within(group).getByRole("button", { name: "EN" }));
    await waitFor(() => expect(document.documentElement.lang).toBe("ru"));
    expect(screen.getByRole("heading", { level: 1, name: "Диалоги" })).toBeInTheDocument();
  });
});

describe("broken channels", () => {
  const broken = {
    id: "ch1",
    type: "telegram_bot",
    name: "Бот магазина",
    status: "error",
    error_code: "token_revoked",
    details: { username: "zerno_shop_bot", display_name: "Зерно", phone_masked: null, delivery: "webhook" },
    created_at: "2026-10-01T10:00:00Z",
    last_inbound_at: null,
  };

  it("tells those who manage channels which one is down and where to fix it", async () => {
    renderApp("/inbox", {
      api: fakeApi({
        routes: {
          "GET /channels": () => json(200, { items: [broken], telegram_user_available: true, encryption_available: true }),
        },
      }),
    });
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Канал «Бот магазина» не работает: Токен бота отозван в @BotFather");
    expect(within(alert).getByRole("link", { name: "Открыть каналы" })).toHaveAttribute("href", "/settings/channels");
  });

  it("does not ask for channels without the right to see them", async () => {
    let asked = 0;
    renderApp("/inbox", {
      api: fakeApi({
        session: operatorSession,
        routes: {
          "GET /channels": () => {
            asked++;
            return json(200, { items: [broken], telegram_user_available: true, encryption_available: true });
          },
        },
      }),
    });
    expect(await screen.findByRole("heading", { level: 1, name: "Диалоги" })).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(asked).toBe(0);
  });
});
