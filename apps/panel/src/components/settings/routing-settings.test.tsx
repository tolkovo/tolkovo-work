import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { fakeApi, json, members, operatorSession, ownerSession, problem } from "../../test/fake-api";
import { renderApp } from "../../test/render-app";

const admin = { ...ownerSession, permissions: [...ownerSession.permissions, "routing.manage"] };

describe("routing settings", () => {
  it("switches to round robin and shows who takes conversations", async () => {
    const user = userEvent.setup();
    const api = fakeApi({
      session: admin,
      routes: { "PUT /routing": async (r) => json(200, await r.json()) },
    });
    renderApp("/settings/routing", { api });
    const manual = await screen.findByRole("radio", { name: "Вручную" });
    expect(manual).toBeChecked();
    expect(manual).toHaveAccessibleDescription(/операторы берут их сами/);

    await user.click(screen.getByRole("radio", { name: "По очереди" }));
    expect(await screen.findByText("Сохранено.")).toBeInTheDocument();
    expect(api.calls.find((c) => c.method === "PUT")?.body).toEqual({ mode: "round_robin", max_chats: null });
    expect(screen.getByRole("radio", { name: "По очереди" })).toBeChecked();
    // Anna is online in the fixtures; Pётр is deactivated.
    const now = screen.getByText(/Сейчас принимает диалоги 1 человек/);
    expect(within(now).getByText("Анна Смирнова")).toBeInTheDocument();
  });

  it("says when nobody is online", async () => {
    const api = fakeApi({
      session: admin,
      routes: {
        "GET /routing": () => json(200, { mode: "round_robin", max_chats: null }),
        "GET /members": () => json(200, { items: members.map((m) => ({ ...m, presence: "away" })) }),
      },
    });
    renderApp("/settings/routing", { api });
    expect(await screen.findByText(/Сейчас никто не принимает диалоги/)).toBeInTheDocument();
  });

  it("sets the limit for everyone and for one operator, with the load", async () => {
    const user = userEvent.setup();
    const api = fakeApi({
      session: admin,
      routes: {
        "GET /routing": () => json(200, { mode: "least_loaded", max_chats: null }),
        "PUT /routing": async (r) => json(200, await r.json()),
        "PUT /routing/members/u-anna": () => new Response(null, { status: 204 }),
      },
    });
    renderApp("/settings/routing", { api });
    expect(await screen.findByRole("radio", { name: "Наименее загруженному" })).toBeChecked();
    const limit = await screen.findByLabelText("Лимит диалогов на оператора");
    expect(limit).toHaveAccessibleDescription(/«Взять» и «Передать» лимит не проверяют/);
    await user.selectOptions(limit, "3");
    expect(await screen.findByText("Сохранено.")).toBeInTheDocument();
    expect(api.calls.find((c) => c.method === "PUT" && c.path === "/routing")?.body).toEqual({ mode: "least_loaded", max_chats: 3 });
    // Anna has one in work out of the workspace's three.
    const row = screen.getByRole("row", { name: /Анна Смирнова/ });
    expect(within(row).getByText("1 из 3")).toBeInTheDocument();
    await user.selectOptions(within(row).getByRole("combobox", { name: "Лимит для Анна Смирнова" }), "2");
    await vi.waitFor(() => expect(api.calls.find((c) => c.path === "/routing/members/u-anna")?.body).toEqual({ max_chats: 2 }));
  });

  it("explains a refusal", async () => {
    const user = userEvent.setup();
    renderApp("/settings/routing", {
      api: fakeApi({ session: admin, routes: { "PUT /routing": () => problem(403, "forbidden") } }),
    });
    await user.click(await screen.findByRole("radio", { name: "По очереди" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Не хватает прав");
  });

  it("is not in the tree without routing.manage", async () => {
    renderApp("/settings/team", { api: fakeApi({ session: operatorSession }) });
    await screen.findByRole("heading", { level: 2, name: "Команда" });
    expect(screen.queryByRole("link", { name: "Распределение" })).not.toBeInTheDocument();
  });
});

describe("operator status", () => {
  const operator = { ...operatorSession, permissions: [...operatorSession.permissions, "conversations.reply"] };

  it("steps away and comes back from the top bar", async () => {
    const user = userEvent.setup();
    const api = fakeApi({
      session: operator,
      routes: { "PUT /me/presence": async (r) => json(200, await r.json()) },
    });
    renderApp("/settings/profile", { api });
    const button = await screen.findByRole("button", { name: "Ваш статус: Онлайн" });
    await user.click(button);
    const menu = screen.getByRole("menu", { name: "Ваш статус: Онлайн" });
    expect(within(menu).getByRole("menuitem", { name: /^Онлайн/ })).toBeDisabled();
    await user.click(within(menu).getByRole("menuitem", { name: /^Перерыв/ }));
    expect(await screen.findByRole("button", { name: "Ваш статус: Перерыв" })).toBeInTheDocument();
    expect(api.calls.find((c) => c.method === "PUT")?.body).toEqual({ status: "away" });
  });

  it("is hidden from members who do not work the inbox", async () => {
    renderApp("/settings/profile", { api: fakeApi({ session: operatorSession }) });
    await screen.findByRole("heading", { level: 2, name: "Профиль" });
    expect(screen.queryByRole("button", { name: /Ваш статус/ })).not.toBeInTheDocument();
  });
});
