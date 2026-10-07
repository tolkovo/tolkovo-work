import type { AccessToken } from "@tolkovo/api-client";
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { fakeApi, json, operatorSession, problem } from "../../test/fake-api";
import { renderApp } from "../../test/render-app";

const mine: AccessToken = {
  id: "t-1",
  user_id: "u-anna",
  user_name: "Анна Смирнова",
  name: "Claude Code, ноутбук",
  scopes: ["tasks:read", "tasks:write"],
  prefix: "tk_pat_Xb3k9Q",
  created_at: "2026-10-01T10:00:00Z",
  expires_at: "2026-12-30T10:00:00Z",
  last_used_at: null,
};

const petrs: AccessToken = { ...mine, id: "t-2", user_id: "u-petr", user_name: "Пётр Иванов", name: "CI", scopes: ["tasks:read"], expires_at: null };

describe("API access", () => {
  it("issues a token, shows it once with the Claude Code command, and lists it", async () => {
    const user = userEvent.setup();
    let tokens: AccessToken[] = [];
    const api = fakeApi({
      session: operatorSession,
      routes: {
        "GET /me/access-tokens": () => json(200, { items: tokens }),
        "POST /me/access-tokens": () => {
          tokens = [mine];
          return json(201, { token: mine, secret: "tk_pat_Xb3k9Qsecret" });
        },
      },
    });
    renderApp("/settings/api", { api });
    expect(await screen.findByRole("heading", { level: 2, name: "Доступ по API" })).toBeInTheDocument();
    expect(await screen.findByText("Токенов пока нет.")).toBeInTheDocument();
    // An operator does not see the members' tokens.
    expect(screen.queryByRole("heading", { name: "Токены сотрудников" })).not.toBeInTheDocument();

    await user.type(screen.getByLabelText("Название"), "Claude Code, ноутбук");
    await user.selectOptions(screen.getByLabelText("Срок действия", { selector: "select" }), "never");
    await user.click(screen.getByRole("button", { name: "Выпустить токен" }));

    expect(await screen.findByLabelText("Токен")).toHaveValue("tk_pat_Xb3k9Qsecret");
    expect(screen.getByLabelText("Подключение к Claude Code")).toHaveValue(
      `claude mcp add --transport http tolkovo ${window.location.origin}/api/v1/mcp --header "Authorization: Bearer tk_pat_Xb3k9Qsecret"`,
    );
    const call = api.calls.find((c) => c.method === "POST" && c.path === "/me/access-tokens");
    expect(call?.body).toEqual({ name: "Claude Code, ноутбук", scopes: ["tasks:write"], expires_in_days: null });

    const list = await screen.findByRole("list", { name: "Мои токены" });
    expect(within(list).getByText("Claude Code, ноутбук")).toBeInTheDocument();
    expect(within(list).getByText("чтение и изменение")).toBeInTheDocument();
    expect(within(list).getByText("Ещё не использовался")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Скрыть" }));
    expect(screen.queryByLabelText("Токен")).not.toBeInTheDocument();
  });

  it("explains the limit of tokens", async () => {
    const user = userEvent.setup();
    renderApp("/settings/api", {
      api: fakeApi({
        session: operatorSession,
        routes: {
          "GET /me/access-tokens": () => json(200, { items: [] }),
          "POST /me/access-tokens": () => problem(409, "too_many_tokens"),
        },
      }),
    });
    await user.type(await screen.findByLabelText("Название"), "Ещё один");
    await user.click(screen.getByRole("button", { name: "Выпустить токен" }));
    expect(await screen.findByText(/не больше 20 токенов/)).toBeInTheDocument();
  });

  it("lets an admin see and revoke a member's token", async () => {
    const user = userEvent.setup();
    let all = [mine, petrs];
    const api = fakeApi({
      routes: {
        "GET /me/access-tokens": () => json(200, { items: [mine] }),
        "GET /access-tokens": () => json(200, { items: all }),
        "DELETE /access-tokens/t-2": () => {
          all = [mine];
          return new Response(null, { status: 204 });
        },
      },
    });
    renderApp("/settings/api", { api });
    const list = await screen.findByRole("list", { name: "Токены сотрудников" });
    expect(within(list).getByText("Сотрудник: Пётр Иванов")).toBeInTheDocument();
    expect(within(list).getByText(/бессрочно/)).toBeInTheDocument();

    await user.click(within(list).getByRole("button", { name: "Отозвать токен «CI»" }));
    expect(await screen.findByText("Токен отозван и больше не работает.")).toBeInTheDocument();
    expect(api.calls.some((c) => c.method === "DELETE" && c.path === "/access-tokens/t-2")).toBe(true);
    expect(await within(await screen.findByRole("list", { name: "Токены сотрудников" })).findAllByRole("listitem")).toHaveLength(1);
  });
});
