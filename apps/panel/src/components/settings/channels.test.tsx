import type { Channel, ChannelList, TelegramLogin } from "@tolkovo/api-client";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { fakeApi, json, operatorSession, problem } from "../../test/fake-api";
import { renderApp } from "../../test/render-app";

const bot: Channel = {
  id: "ch-bot",
  type: "telegram_bot",
  name: "Зерно · поддержка",
  status: "active",
  error_code: null,
  details: { username: "zerno_help_bot", display_name: "Зерно · поддержка", phone_masked: null, delivery: "webhook" },
  created_at: "2026-10-04T09:00:00Z",
};

const pendingAccount: Channel = {
  id: "ch-user",
  type: "telegram_user",
  name: "Telegram +79•••••••67",
  status: "pending",
  error_code: null,
  details: { username: null, display_name: null, phone_masked: "+79•••••••67", delivery: undefined },
  created_at: "2026-10-04T09:05:00Z",
};

const account: Channel = {
  ...pendingAccount,
  name: "Марина Ковалёва",
  status: "active",
  details: { username: "marina_zerno", display_name: "Марина Ковалёва", phone_masked: "+79•••••••67" },
};

const list = (items: Channel[], extra: Partial<ChannelList> = {}) =>
  json(200, { items, telegram_user_available: true, encryption_available: true, ...extra });

describe("channels settings", () => {
  it("lists channels with what tells them apart and hides the page from operators", async () => {
    renderApp("/settings/channels", { api: fakeApi({ routes: { "GET /channels": () => list([bot, account]) } }) });
    const items = within(await screen.findByRole("list", { name: "Подключённые каналы" })).getAllByRole("listitem");
    expect(items).toHaveLength(2);
    expect(items[0]).toHaveTextContent("Зерно · поддержка");
    expect(items[0]).toHaveTextContent("Работает");
    expect(items[0]).toHaveTextContent("Telegram-бот · @zerno_help_bot · сообщения приходят по webhook");
    expect(items[1]).toHaveTextContent("Личный аккаунт · @marina_zerno · +79•••••••67");
  });

  it("connects a bot by its token", async () => {
    const user = userEvent.setup();
    let channels: Channel[] = [];
    const api = fakeApi({
      routes: {
        "GET /channels": () => list(channels),
        "POST /channels/telegram-bot": async (request) => {
          const body = (await request.json()) as { token: string };
          if (body.token !== "123456789:AAH-token") return problem(422, "telegram_token_invalid");
          channels = [bot];
          return json(201, bot);
        },
      },
    });
    renderApp("/settings/channels", { api });
    expect(await screen.findByText("Каналов пока нет.")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Подключить Telegram" }));
    expect(screen.getByRole("radio", { name: /Бот/ })).toBeChecked();
    await user.click(screen.getByRole("button", { name: "Дальше" }));

    const token = screen.getByLabelText("Токен бота");
    await user.type(token, "111:wrong");
    await user.click(screen.getByRole("button", { name: "Подключить бота" }));
    expect(await screen.findByText("Telegram не знает такой токен. Скопируйте его из @BotFather ещё раз.")).toBeInTheDocument();
    expect(token).toHaveAttribute("aria-invalid", "true");

    await user.clear(token);
    await user.type(token, "123456789:AAH-token");
    await user.click(screen.getByRole("button", { name: "Подключить бота" }));
    expect(await screen.findByText("Бот «Зерно · поддержка» подключён. Новые сообщения появятся во «Входящих».")).toBeInTheDocument();
    expect(await screen.findByRole("list", { name: "Подключённые каналы" })).toHaveTextContent("Зерно · поддержка");
    expect(api.calls.filter((c) => c.path === "/channels/telegram-bot").at(-1)?.body).toEqual({ token: "123456789:AAH-token" });
  });

  it("connects a personal account: warning, phone, code, cloud password", async () => {
    const user = userEvent.setup();
    let channels: Channel[] = [];
    const login = (next: TelegramLogin["next"], channel: Channel): TelegramLogin => ({
      channel,
      next,
      ...(next === "code" ? { code_via: "app" as const } : {}),
    });
    const api = fakeApi({
      routes: {
        "GET /channels": () => list(channels),
        "POST /channels/telegram-user": () => {
          channels = [pendingAccount];
          return json(201, login("code", pendingAccount));
        },
        "POST /channels/ch-user/telegram-user/code": async (request) => {
          const { code } = (await request.json()) as { code: string };
          return code === "22222" ? json(200, login("password", pendingAccount)) : problem(422, "telegram_code_invalid");
        },
        "POST /channels/ch-user/telegram-user/password": async (request) => {
          const { password } = (await request.json()) as { password: string };
          if (password !== "correct horse") return problem(422, "telegram_password_invalid");
          channels = [account];
          return json(200, login("done", account));
        },
      },
    });
    renderApp("/settings/channels", { api });
    await user.click(await screen.findByRole("button", { name: "Подключить Telegram" }));
    await user.click(screen.getByRole("radio", { name: /Личный аккаунт/ }));
    await user.click(screen.getByRole("button", { name: "Дальше" }));

    // The risks come first; the code is requested only after the admin agrees.
    expect(screen.getByText(/все его личные переписки увидят операторы/)).toBeInTheDocument();
    await user.type(screen.getByLabelText("Номер телефона"), "+7 900 123-45-67");
    const getCode = screen.getByRole("button", { name: "Получить код" });
    expect(getCode).toBeDisabled();
    await user.click(screen.getByRole("checkbox", { name: "Понимаю, подключаю рабочий аккаунт" }));
    await user.click(getCode);

    expect(await screen.findByRole("heading", { name: "Код из Telegram" })).toHaveFocus();
    expect(screen.getByText(/откройте на аккаунте \+7 900 123-45-67 чат «Telegram»/)).toBeInTheDocument();
    await user.type(screen.getByLabelText("Код"), "00000");
    await user.click(screen.getByRole("button", { name: "Подтвердить" }));
    expect(await screen.findByText(/Неверный код/)).toBeInTheDocument();
    await user.clear(screen.getByLabelText("Код"));
    await user.type(screen.getByLabelText("Код"), "22222");
    await user.click(screen.getByRole("button", { name: "Подтвердить" }));

    const password = await screen.findByLabelText("Облачный пароль", { selector: "input" });
    expect(password).toHaveAttribute("type", "password");
    await user.type(password, "wrong");
    await user.click(screen.getByRole("button", { name: "Войти" }));
    expect(await screen.findByText("Неверный облачный пароль.")).toBeInTheDocument();
    expect(password).toHaveValue("");
    // user-event keeps its own copy of the typed value; form.reset() does not reach it.
    await user.clear(password);
    await user.type(password, "correct horse");
    await user.click(screen.getByRole("button", { name: "Войти" }));
    expect(await screen.findByText(/Аккаунт «Марина Ковалёва» подключён/)).toBeInTheDocument();
    expect(api.calls.find((c) => c.path === "/channels/telegram-user")?.body).toEqual({ phone: "+7 900 123-45-67" });
  });

  it("cancels a login left half-way when the wizard is closed", async () => {
    const user = userEvent.setup();
    const api = fakeApi({
      routes: {
        "GET /channels": () => list([]),
        "POST /channels/telegram-user": () => json(201, { channel: pendingAccount, next: "code", code_via: "sms" }),
        "DELETE /channels/ch-user": () => new Response(null, { status: 204 }),
      },
    });
    renderApp("/settings/channels", { api });
    await user.click(await screen.findByRole("button", { name: "Подключить Telegram" }));
    await user.click(screen.getByRole("radio", { name: /Личный аккаунт/ }));
    await user.click(screen.getByRole("button", { name: "Дальше" }));
    await user.click(screen.getByRole("checkbox", { name: "Понимаю, подключаю рабочий аккаунт" }));
    await user.type(screen.getByLabelText("Номер телефона"), "+79001234567");
    await user.click(screen.getByRole("button", { name: "Получить код" }));
    expect(await screen.findByText("Код отправлен по SMS на +79001234567.")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Закрыть" }));
    await waitFor(() => expect(api.calls.some((c) => c.method === "DELETE" && c.path === "/channels/ch-user")).toBe(true));
  });

  it("explains why a personal account or any channel cannot be connected on this server", async () => {
    const user = userEvent.setup();
    renderApp("/settings/channels", {
      api: fakeApi({ routes: { "GET /channels": () => list([], { telegram_user_available: false }) } }),
    });
    await user.click(await screen.findByRole("button", { name: "Подключить Telegram" }));
    expect(screen.getByRole("radio", { name: /Личный аккаунт/ })).toBeDisabled();
    expect(screen.getByText(/TOLKOVO_TELEGRAM_API_ID/)).toBeInTheDocument();
  });

  it("disconnects after an in-place confirmation", async () => {
    const user = userEvent.setup();
    let channels = [bot];
    const api = fakeApi({
      routes: {
        "GET /channels": () => list(channels),
        "DELETE /channels/ch-bot": () => {
          channels = [];
          return new Response(null, { status: 204 });
        },
      },
    });
    renderApp("/settings/channels", { api });
    await user.click(await screen.findByRole("button", { name: "Отключить" }));
    expect(screen.getByText(/Бот перестанет присылать сообщения в Tolkovo/)).toBeInTheDocument();
    const confirm = within(screen.getByRole("group", { name: "Отключить" })).getByRole("button", { name: "Отключить" });
    expect(confirm).toHaveFocus();
    await user.click(confirm);
    expect(await screen.findByText("Канал «Зерно · поддержка» отключён.")).toBeInTheDocument();
    expect(await screen.findByText("Каналов пока нет.")).toBeInTheDocument();
  });

  it("without the encryption key offers nothing to connect", async () => {
    renderApp("/settings/channels", {
      api: fakeApi({ routes: { "GET /channels": () => list([], { encryption_available: false }) } }),
    });
    expect(await screen.findByText(/не задан ключ шифрования/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Подключить Telegram" })).not.toBeInTheDocument();
  });

  it("is not in an operator's settings", async () => {
    renderApp("/settings/team", { api: fakeApi({ session: operatorSession }) });
    const nav = await screen.findByRole("navigation", { name: "Разделы настроек" });
    expect(within(nav).queryByRole("link", { name: "Каналы" })).not.toBeInTheDocument();
  });
});
