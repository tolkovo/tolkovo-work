import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { fakeApi, json, operatorSession, ownerSession, problem } from "../../test/fake-api";
import { renderApp } from "../../test/render-app";

describe("workspace settings", () => {
  it("opens General from /settings", async () => {
    const { router } = renderApp("/settings");
    expect(await screen.findByRole("heading", { level: 2, name: "Общие" })).toBeInTheDocument();
    expect(router.state.location.pathname).toBe("/settings/general");
    expect(screen.getByLabelText("Название")).toHaveValue("Зерно");
    expect(screen.getByLabelText("Часовой пояс")).toHaveValue("Europe/Moscow");
  });

  it("saves changes with PATCH /workspace and the CSRF token", async () => {
    const user = userEvent.setup();
    const api = fakeApi({
      routes: {
        "PATCH /workspace": () => json(200, { ...ownerSession.workspace, name: "Зерно и Ко", locale: "en" }),
      },
    });
    renderApp("/settings/general", { api });
    const name = await screen.findByLabelText("Название");

    await user.clear(name);
    await user.type(name, "Зерно и Ко");
    await user.selectOptions(screen.getByLabelText("Язык пространства"), "en");
    await user.click(screen.getByRole("button", { name: "Сохранить" }));

    expect(await screen.findByRole("status")).toHaveTextContent("Сохранено");
    const call = api.calls.find((c) => c.method === "PATCH");
    expect(call?.body).toEqual({ name: "Зерно и Ко", locale: "en", timezone: "Europe/Moscow" });
    expect(call?.headers.get("X-CSRF-Token")).toBe("csrf-owner");
    // The session cache follows, so the top bar shows the new name without a reload.
    expect(await screen.findByText("Зерно и Ко", { selector: "header *" })).toBeInTheDocument();
  });

  it("does not send an empty name", async () => {
    const user = userEvent.setup();
    const api = fakeApi();
    renderApp("/settings/general", { api });
    const name = await screen.findByLabelText("Название");
    await user.clear(name);
    await user.click(screen.getByRole("button", { name: "Сохранить" }));
    expect(name).toHaveAccessibleDescription("Заполните поле.");
    expect(api.calls.some((c) => c.method === "PATCH")).toBe(false);
  });

  it("explains a server refusal", async () => {
    const user = userEvent.setup();
    renderApp("/settings/general", { api: fakeApi({ routes: { "PATCH /workspace": () => problem(403, "forbidden") } }) });
    await user.click(await screen.findByRole("button", { name: "Сохранить" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Не хватает прав");
  });

  it("is read-only for an operator", async () => {
    renderApp("/settings/general", { api: fakeApi({ session: operatorSession }) });
    expect(await screen.findByText("Менять настройки пространства могут владелец и администратор.")).toBeInTheDocument();
    expect(screen.getByLabelText("Название")).toBeDisabled();
    expect(screen.queryByRole("button", { name: "Сохранить" })).not.toBeInTheDocument();
  });
});

describe("team", () => {
  it("lists members with role and status, marking the current user", async () => {
    renderApp("/settings/team");
    const table = await screen.findByRole("table");
    const rows = within(table).getAllByRole("row").slice(1);
    expect(rows.map((r) => within(r).getAllByRole("cell").map((c) => c.textContent))).toEqual([
      ["Анна Смирнова(вы)", "anna@zerno.test", "Владелец", "Активен"],
      ["Пётр Иванов", "petr@zerno.test", "Оператор", "Отключён"],
    ]);
    expect(screen.getByText("2 участника")).toBeInTheDocument();
  });

  it("offers a retry when the list fails to load", async () => {
    const user = userEvent.setup();
    let fail = true;
    const api = fakeApi({
      routes: {
        "GET /members": () =>
          fail ? problem(403, "forbidden") : json(200, { items: [] }),
      },
    });
    renderApp("/settings/team", { api });
    // The invite form keeps its own (empty) alert region on the page, so find the text.
    expect(await screen.findByText(/Не хватает прав/)).toBeInTheDocument();
    fail = false;
    await user.click(screen.getByRole("button", { name: "Повторить" }));
    expect(await screen.findByText("0 участников")).toBeInTheDocument();
  });
});

describe("onboarding", () => {
  it("confirms language and time zone, then leads to the inbox", async () => {
    const user = userEvent.setup();
    const api = fakeApi({ routes: { "PATCH /workspace": () => json(200, ownerSession.workspace) } });
    renderApp("/onboarding", { api });
    expect(await screen.findByText("Готово 1 из 2")).toBeInTheDocument();
    expect(screen.queryByLabelText("Название")).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Подтвердить" }));

    expect(await screen.findByText("Готово 2 из 2")).toBeInTheDocument();
    expect(api.calls.find((c) => c.method === "PATCH")?.body).toEqual({ locale: "ru", timezone: "Europe/Moscow" });
    expect(screen.getByRole("link", { name: "Перейти к диалогам" })).toHaveAttribute("href", "/inbox");
  });
});
