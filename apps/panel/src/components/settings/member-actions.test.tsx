import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { Member } from "@tolkovo/api-client";
import { describe, expect, it } from "vitest";

import { fakeApi, json, operatorSession, problem } from "../../test/fake-api";
import { renderApp } from "../../test/render-app";

const team: Member[] = [
  { user_id: "u-anna", email: "anna@zerno.test", name: "Анна Смирнова", role: "owner", active: true, presence: "online" },
  { user_id: "u-olga", email: "olga@zerno.test", name: "Ольга Белова", role: "admin", active: true, presence: "online" },
  { user_id: "u-petr", email: "petr@zerno.test", name: "Пётр Иванов", role: "operator", active: true, presence: "away" },
];

describe("team members", () => {
  it("changes the role of a member, not of the owner or oneself", async () => {
    const user = userEvent.setup();
    let items = team;
    const api = fakeApi({
      routes: {
        "GET /members": () => json(200, { items }),
        "PATCH /members/u-petr": () => {
          items = items.map((m) => (m.user_id === "u-petr" ? { ...m, role: "admin" } : m));
          return json(200, items[2]);
        },
      },
    });
    renderApp("/settings/team", { api });

    const role = await screen.findByRole("combobox", { name: "Роль: Пётр Иванов" });
    expect(screen.queryByRole("combobox", { name: "Роль: Анна Смирнова" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Убрать из команды: Анна Смирнова" })).not.toBeInTheDocument();

    await user.selectOptions(role, "admin");
    expect(await screen.findByText("Пётр Иванов теперь — Администратор.")).toBeInTheDocument();
    const call = api.calls.find((c) => c.method === "PATCH" && c.path === "/members/u-petr");
    expect(call?.body).toEqual({ role: "admin" });
    expect(call?.headers.get("X-CSRF-Token")).toBe("csrf-owner");
  });

  it("removes a member and hands their work to the chosen colleagues", async () => {
    const user = userEvent.setup();
    let items = team;
    const api = fakeApi({
      routes: {
        "GET /members": () => json(200, { items }),
        "POST /members/u-petr/deactivate": () => {
          items = items.map((m) => (m.user_id === "u-petr" ? { ...m, active: false, presence: "offline" } : m));
          return new Response(null, { status: 204 });
        },
      },
    });
    renderApp("/settings/team", { api });

    const opener = await screen.findByRole("button", { name: "Убрать из команды: Пётр Иванов" });
    await user.click(opener);
    const dialog = screen.getByRole("dialog", { name: "Убрать Пётр Иванов из команды?" });
    const convs = within(dialog).getByLabelText("Его открытые диалоги");
    expect(convs).toHaveFocus();
    // The member themselves takes nothing over.
    expect(within(convs).queryByRole("option", { name: "Пётр Иванов" })).not.toBeInTheDocument();
    await user.selectOptions(convs, "u-olga");
    await user.click(within(dialog).getByRole("button", { name: "Убрать из команды" }));

    expect(await screen.findByText("Пётр Иванов больше не в команде.")).toBeInTheDocument();
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    const call = api.calls.find((c) => c.method === "POST" && c.path === "/members/u-petr/deactivate");
    expect(call?.body).toEqual({ conversations_to: "u-olga", tasks_to: null });
    expect(await screen.findByText("Отключён")).toBeInTheDocument();
  });

  it("keeps the dialog open with the reason when the server refuses, and Escape returns focus", async () => {
    const user = userEvent.setup();
    const api = fakeApi({
      routes: {
        "GET /members": () => json(200, { items: team }),
        "POST /members/u-petr/deactivate": () => problem(403, "protected_member"),
      },
    });
    renderApp("/settings/team", { api });

    const opener = await screen.findByRole("button", { name: "Убрать из команды: Пётр Иванов" });
    await user.click(opener);
    const dialog = screen.getByRole("dialog");
    await user.click(within(dialog).getByRole("button", { name: "Убрать из команды" }));
    expect(await within(dialog).findByText("Свою роль и роль владельца здесь не изменить.")).toBeInTheDocument();
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(opener).toHaveFocus();
  });

  it("shows no actions to an operator", async () => {
    const api = fakeApi({ session: operatorSession, routes: { "GET /members": () => json(200, { items: team }) } });
    renderApp("/settings/team", { api });
    expect(await screen.findByText("Ольга Белова")).toBeInTheDocument();
    expect(screen.queryByRole("combobox", { name: /^Роль:/ })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /^Убрать из команды/ })).not.toBeInTheDocument();
  });
});
