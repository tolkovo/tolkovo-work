import type { AppChannelCreated, Channel, ChannelList, IngestKey, TelegramLogin, WidgetChannelCreated } from "@tolkovo/api-client";
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
  last_inbound_at: null,
  created_at: "2026-10-04T09:00:00Z",
};

const pendingAccount: Channel = {
  id: "ch-user",
  type: "telegram_user",
  name: "Telegram +79•••••••67",
  status: "pending",
  error_code: null,
  details: { username: null, display_name: null, phone_masked: "+79•••••••67", delivery: undefined },
  last_inbound_at: null,
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
    expect(items[0]).toHaveTextContent("Сообщений ещё не было.");
  });

  it("shows when a channel last received a message and why a broken one stopped", async () => {
    const working: Channel = { ...bot, last_inbound_at: "2026-10-04T12:30:00Z" };
    const revoked: Channel = { ...account, status: "error", error_code: "session_revoked" };
    const odd: Channel = { ...bot, id: "ch-odd", name: "Старый бот", status: "error", error_code: "something_new" };
    renderApp("/settings/channels", { api: fakeApi({ routes: { "GET /channels": () => list([working, revoked, odd]) } }) });
    const items = within(await screen.findByRole("list", { name: "Подключённые каналы" })).getAllByRole("listitem");
    expect(items[0]).toHaveTextContent(/Последнее сообщение: 4 окт\.?,? \d{2}:30/);
    expect(items[1]).toHaveTextContent("Ошибка");
    expect(items[1]).toHaveTextContent("Сеанс Tolkovo завершён в Telegram («Устройства»), сообщения не приходят.");
    expect(items[2]).toHaveTextContent("Канал не работает. Отключите его и подключите заново.");
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
          // The customer writes at once: the check sees the first message.
          channels = [{ ...bot, last_inbound_at: "2026-10-04T12:30:00Z" }];
          return json(201, bot);
        },
      },
    });
    renderApp("/settings/channels", { api });
    expect(await screen.findByText("Каналов пока нет.")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Подключить канал" }));
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
    expect(await screen.findByRole("heading", { name: "Проверка связи" })).toHaveFocus();
    expect(screen.getByText("Проверим связь: откройте в Telegram @zerno_help_bot и отправьте любое сообщение.")).toBeInTheDocument();
    expect(await screen.findByText("Сообщение получено — оно уже во «Входящих».")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Открыть «Входящие»" })).toHaveAttribute("href", "/inbox");
    await user.click(screen.getByRole("button", { name: "Готово" }));
    expect(await screen.findByText("Бот «Зерно · поддержка» подключён. Новые сообщения появятся во «Входящих».")).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Проверка связи" })).not.toBeInTheDocument();
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
    await user.click(await screen.findByRole("button", { name: "Подключить канал" }));
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
    // Nobody has written yet: the wizard waits, and the check can be left for later.
    expect(screen.getByText("Проверим связь: напишите этому аккаунту в личку с другого аккаунта Telegram.")).toBeInTheDocument();
    expect(screen.getByText("Ждём сообщение…")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Проверю позже" }));
    expect(screen.queryByText("Ждём сообщение…")).not.toBeInTheDocument();
    expect(screen.getByText(/Аккаунт «Марина Ковалёва» подключён/)).toBeInTheDocument();
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
    await user.click(await screen.findByRole("button", { name: "Подключить канал" }));
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
    await user.click(await screen.findByRole("button", { name: "Подключить канал" }));
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
    expect(screen.queryByRole("button", { name: "Подключить канал" })).not.toBeInTheDocument();
  });

  it("is not in an operator's settings", async () => {
    renderApp("/settings/team", { api: fakeApi({ session: operatorSession }) });
    const nav = await screen.findByRole("navigation", { name: "Разделы настроек" });
    expect(within(nav).queryByRole("link", { name: "Каналы" })).not.toBeInTheDocument();
  });
  it("connects an app: the key and the identity secret are shown once, then keys, secret and limits", async () => {
    const user = userEvent.setup();
    const limits = { max_file_mb: 25, max_files: 5, max_report_mb: 50, retention_days: 30, daily_reports: 500, daily_mb: 2000 };
    const app: Channel = {
      id: "ch-app",
      type: "app",
      name: "Зерно для Windows",
      status: "active",
      error_code: null,
      details: { username: null, display_name: null, phone_masked: null, app_limits: limits },
      last_inbound_at: null,
      created_at: "2026-10-05T09:00:00Z",
    };
    const first: IngestKey = {
      id: "key-1",
      name: "Зерно для Windows",
      prefix: "tk_ingest_Xb3k9Q",
      created_at: "2026-10-05T09:00:00Z",
      last_used_at: null,
      revoked_at: null,
    };
    const created: AppChannelCreated = {
      channel: app,
      ingest_key: { key: first, secret: "tk_ingest_Xb3k9Q-whole-key" },
      identity_secret: "tk_identity_secret-value",
    };
    let channels: Channel[] = [];
    let keys: IngestKey[] = [];
    const api = fakeApi({
      routes: {
        "GET /channels": () => list(channels),
        "POST /channels/app": () => {
          channels = [app];
          keys = [first];
          return json(201, created);
        },
        "GET /channels/ch-app/ingest-keys": () => json(200, { items: keys }),
        "POST /channels/ch-app/ingest-keys": () => {
          const second = { ...first, id: "key-2", name: "Сборка 2.4", prefix: "tk_ingest_Pq7Lm2" };
          keys = [second, ...keys];
          return json(201, { key: second, secret: "tk_ingest_Pq7Lm2-second-key" });
        },
        "DELETE /channels/ch-app/ingest-keys/key-1": () => {
          keys = keys.map((k) => (k.id === "key-1" ? { ...k, revoked_at: "2026-10-05T10:00:00Z" } : k));
          return new Response(null, { status: 204 });
        },
        "POST /channels/ch-app/identity-secret": () => json(200, { identity_secret: "tk_identity_new-secret" }),
        "PUT /channels/ch-app/app-limits": async (request) => {
          const body = (await request.json()) as typeof limits;
          channels = [{ ...app, details: { ...app.details, app_limits: body } }];
          return json(200, channels[0]);
        },
      },
    });
    renderApp("/settings/channels", { api });
    await user.click(await screen.findByRole("button", { name: "Подключить канал" }));
    await user.click(screen.getByRole("radio", { name: /Приложение/ }));
    await user.click(screen.getByRole("button", { name: "Дальше" }));
    expect(screen.getByRole("heading", { name: "Подключение приложения" })).toHaveFocus();
    await user.type(screen.getByLabelText("Название в панели"), "Зерно для Windows");
    await user.click(screen.getByRole("button", { name: "Создать канал" }));

    expect(await screen.findByRole("heading", { name: "Ключи приложения" })).toHaveFocus();
    expect(screen.getByLabelText("Ключ приёма")).toHaveValue("tk_ingest_Xb3k9Q-whole-key");
    expect(screen.getByLabelText("Секрет подписи личности")).toHaveValue("tk_identity_secret-value");
    expect(screen.getByRole("note")).toHaveTextContent("показываются только сейчас");
    await user.click(screen.getByRole("button", { name: "Готово, ключи сохранены" }));
    expect(await screen.findByText("Канал «Зерно для Windows» создан. Передайте ключ разработчику приложения.")).toBeInTheDocument();
    expect(screen.queryByDisplayValue("tk_ingest_Xb3k9Q-whole-key")).not.toBeInTheDocument();
    expect(api.calls.find((c) => c.path === "/channels/app")?.body).toEqual({ name: "Зерно для Windows" });

    const row = within(await screen.findByRole("list", { name: "Подключённые каналы" })).getAllByRole("listitem")[0]!;
    expect(row).toHaveTextContent("Приложение");
    const toggle = within(row).getByRole("button", { name: "Ключи и лимиты" });
    await user.click(toggle);
    expect(toggle).toHaveAttribute("aria-expanded", "true");
    const keyList = await within(row).findByRole("list", { name: "Ключи приёма" });
    expect(keyList).toHaveTextContent("tk_ingest_Xb3k9Q…");
    expect(keyList).toHaveTextContent("отчётов ещё не было");
    expect(keyList).not.toHaveTextContent("whole-key");

    await user.type(within(row).getByLabelText("Новый ключ"), "Сборка 2.4");
    await user.click(within(row).getByRole("button", { name: "Выпустить ключ" }));
    expect(await within(row).findByDisplayValue("tk_ingest_Pq7Lm2-second-key")).toBeInTheDocument();
    await user.click(within(row).getByRole("button", { name: "Скрыть" }));
    expect(within(row).queryByDisplayValue("tk_ingest_Pq7Lm2-second-key")).not.toBeInTheDocument();

    const firstKey = (await within(row).findAllByRole("listitem")).find((li) => li.textContent?.includes("Xb3k9Q"))!;
    await user.click(within(firstKey).getByRole("button", { name: "Отозвать" }));
    expect(within(firstKey).getByText(/Приложения с этим ключом сразу перестанут/)).toBeInTheDocument();
    expect(within(firstKey).getByRole("button", { name: "Отозвать" })).toHaveFocus();
    await user.click(within(firstKey).getByRole("button", { name: "Отозвать" }));
    await waitFor(() => expect(within(row).getByRole("list", { name: "Ключи приёма" })).toHaveTextContent(/отозван 5 окт/));

    await user.click(within(row).getByRole("button", { name: "Заменить секрет" }));
    expect(within(row).getByText(/Старый секрет перестанет работать сразу/)).toBeInTheDocument();
    await user.click(within(row).getByRole("button", { name: "Заменить секрет" }));
    expect(await within(row).findByDisplayValue("tk_identity_new-secret")).toBeInTheDocument();

    const retention = within(row).getByLabelText("Хранить файлы, дней");
    expect(retention).toHaveValue(30);
    await user.clear(retention);
    await user.type(retention, "90");
    await user.click(within(row).getByRole("button", { name: "Сохранить лимиты" }));
    expect(await within(row).findByText("Лимиты сохранены.")).toBeInTheDocument();
    expect(api.calls.find((c) => c.path === "/channels/ch-app/app-limits")?.body).toEqual({ ...limits, retention_days: 90 });
  });

  it("connects a widget: the pages, the key with the install code, the identity secret once", async () => {
    const user = userEvent.setup();
    const widget: Channel = {
      id: "ch-widget",
      type: "widget",
      name: "Чат в приложении",
      status: "active",
      error_code: null,
      details: {
        username: null,
        display_name: null,
        phone_masked: null,
        widget_key: "tk_widget_public-key",
        allowed_origins: ["app://zerno", "https://shop.example.com"],
      },
      last_inbound_at: null,
      created_at: "2026-10-05T09:00:00Z",
    };
    const created: WidgetChannelCreated = { channel: widget, identity_secret: "tk_identity_widget-secret" };
    let channels: Channel[] = [];
    const api = fakeApi({
      routes: {
        "GET /channels": () => list(channels),
        "POST /channels/widget": () => {
          channels = [widget];
          return json(201, created);
        },
        "PUT /channels/ch-widget/widget-origins": async (request) => {
          const body = (await request.json()) as { allowed_origins: string[] };
          if (body.allowed_origins.includes("*")) {
            return problem(400, "invalid_request");
          }
          channels = [{ ...widget, details: { ...widget.details, allowed_origins: body.allowed_origins } }];
          return json(200, channels[0]);
        },
      },
    });
    renderApp("/settings/channels", { api });
    await user.click(await screen.findByRole("button", { name: "Подключить канал" }));
    await user.click(screen.getByRole("radio", { name: /Виджет/ }));
    await user.click(screen.getByRole("button", { name: "Дальше" }));
    expect(screen.getByRole("heading", { name: "Подключение виджета" })).toHaveFocus();
    await user.type(screen.getByLabelText("Название в панели"), "Чат в приложении");
    await user.type(screen.getByLabelText("Адреса страниц"), "app://zerno\n\nhttps://shop.example.com, ");
    await user.click(screen.getByRole("button", { name: "Создать виджет" }));

    expect(await screen.findByRole("heading", { name: "Ключи виджета" })).toHaveFocus();
    expect(api.calls.find((c) => c.path === "/channels/widget")?.body).toEqual({
      name: "Чат в приложении",
      allowed_origins: ["app://zerno", "https://shop.example.com"],
    });
    expect(screen.getByLabelText("Ключ виджета")).toHaveValue("tk_widget_public-key");
    expect(screen.getByLabelText("Код для приложения")).toHaveTextContent('widgetKey: "tk_widget_public-key"');
    expect(screen.getByLabelText("Секрет подписи личности")).toHaveValue("tk_identity_widget-secret");
    await user.click(screen.getByRole("button", { name: "Готово, ключи сохранены" }));
    expect(await screen.findByText("Виджет «Чат в приложении» создан. Передайте ключ и код разработчику.")).toBeInTheDocument();
    expect(screen.queryByDisplayValue("tk_identity_widget-secret")).not.toBeInTheDocument();

    const row = within(await screen.findByRole("list", { name: "Подключённые каналы" })).getAllByRole("listitem")[0]!;
    expect(row).toHaveTextContent("Виджет");
    await user.click(within(row).getByRole("button", { name: "Ключ и адреса" }));
    // The key is public: it stays in the settings, the secret does not.
    expect(within(row).getByLabelText("Ключ виджета")).toHaveValue("tk_widget_public-key");
    expect(within(row).queryByDisplayValue("tk_identity_widget-secret")).not.toBeInTheDocument();
    const origins = within(row).getByLabelText("Адреса страниц");
    expect(origins).toHaveValue("app://zerno\nhttps://shop.example.com");

    await user.clear(origins);
    await user.type(origins, "*");
    await user.click(within(row).getByRole("button", { name: "Сохранить адреса" }));
    await waitFor(() => expect(within(row).getAllByRole("alert").some((a) => a.textContent)).toBe(true));
    expect(within(row).queryByText(/Адреса сохранены/)).not.toBeInTheDocument();
    await user.clear(origins);
    await user.type(origins, "app://zerno");
    await user.click(within(row).getByRole("button", { name: "Сохранить адреса" }));
    expect(await within(row).findByText(/Адреса сохранены/)).toBeInTheDocument();
    expect(api.calls.filter((c) => c.path === "/channels/ch-widget/widget-origins").at(-1)?.body).toEqual({
      allowed_origins: ["app://zerno"],
    });
  });
});
