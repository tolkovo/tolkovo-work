import type { Contact, Conversation, Message, Tag } from "@tolkovo/api-client";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { CARD_SECTIONS_KEY, useCardSections } from "../../lib/inbox/card-sections";
import { resetDrafts } from "../../lib/inbox/drafts";
import { resetSignals, signalSender } from "../../lib/realtime/signals";
import { fakeApi, json, ownerSession, problem, type FakeRoutes } from "../../test/fake-api";
import { contact, conv, inboxOperator, msg } from "../../test/inbox-fixtures";
import { renderApp } from "../../test/render-app";

const me = inboxOperator.user.id;

type State = { conversation: Conversation; feed: Message[]; tags: Tag[]; contact: Contact; history: Conversation[] };

/**
 * A small stateful server for one conversation: sending adds to the feed, changes bump the
 * version, as api/openapi.yaml describes.
 */
function server(initial: Partial<State> = {}, extra: FakeRoutes = {}, session = inboxOperator) {
  const state: State = {
    conversation: conv("c1"),
    feed: [msg("m1", { body: "С карты списали два раза" })],
    tags: [],
    contact: contact("k-c1"),
    history: [],
    ...initial,
  };
  const bump = (patch: Partial<Conversation>) => {
    state.conversation = { ...state.conversation, ...patch, version: state.conversation.version + 1 };
    return json(200, state.conversation);
  };
  const api = fakeApi({
    session,
    routes: {
      "GET /conversations": (request) =>
        new URL(request.url).searchParams.has("contact_id")
          ? json(200, { items: [state.conversation, ...state.history], next_cursor: null })
          : json(200, { items: [state.conversation], next_cursor: null }),
      "GET /contacts/k-c1": () => json(200, state.contact),
      "PATCH /contacts/k-c1": async (request) => {
        const body = (await request.json()) as Partial<Contact>;
        const cleared = (v: string | null | undefined, was: string | null) => (v === undefined ? was : v || null);
        state.contact = {
          ...state.contact,
          name: body.name ?? state.contact.name,
          email: cleared(body.email, state.contact.email),
          phone: cleared(body.phone, state.contact.phone),
          locale: cleared(body.locale, state.contact.locale),
          updated_at: new Date().toISOString(),
        };
        return json(200, state.contact);
      },
      "GET /conversations/c1": () => json(200, state.conversation),
      "GET /conversations/c1/messages": () => json(200, { items: [...state.feed].reverse(), next_cursor: null }),
      "GET /tags": () => json(200, { items: state.tags }),
      "POST /conversations/c1/messages": async (request) => {
        const body = (await request.json()) as { body: string; private?: boolean };
        const m = msg(`m${state.feed.length + 1}`, {
          body: body.body,
          sender_type: "operator",
          sender_id: me,
          direction: "out",
          kind: body.private ? "note" : "text",
          is_private: body.private ?? false,
          created_at: new Date().toISOString(),
        });
        state.feed.push(m);
        return json(201, m);
      },
      "POST /conversations/c1/status": async (request) => {
        const body = (await request.json()) as { status: Conversation["status"]; version?: number };
        if (body.version !== undefined && body.version !== state.conversation.version) return problem(409, "version_conflict");
        return bump({ status: body.status });
      },
      "PUT /conversations/c1/assignee": async (request) => {
        const body = (await request.json()) as { assignee_id: string | null };
        return bump({ assignee_id: body.assignee_id, status: body.assignee_id ? "assigned" : "queued" });
      },
      "PUT /conversations/c1/tags": async (request) => {
        const body = (await request.json()) as { tag_ids: string[] };
        return bump({ tag_ids: body.tag_ids });
      },
      "POST /conversations/c1/read": () => new Response(null, { status: 204 }),
      ...extra,
    },
  });
  return { api, state };
}

const calls = (api: ReturnType<typeof fakeApi>, method: string, path: string) =>
  api.calls.filter((c) => c.method === method && c.path === path);

/** The open conversation, apart from the customer card that repeats its name and status. */
const conversationRegion = () => screen.findByRole("region", { name: /Марина Ковалёва\s*№7/ });

describe("composer", () => {
  afterEach(() => {
    resetDrafts();
    resetSignals();
  });

  it("sends a reply with Enter, keeps Shift+Enter as a new line and shows it in the feed", async () => {
    const user = userEvent.setup();
    const { api } = server();
    renderApp("/inbox/c1", { api });
    const field = await screen.findByRole("textbox", { name: "Ответ клиенту" });
    await user.type(field, "Проверяю{Shift>}{Enter}{/Shift}сейчас{Enter}");

    await waitFor(() => expect(calls(api, "POST", "/conversations/c1/messages")).toHaveLength(1));
    const sent = calls(api, "POST", "/conversations/c1/messages")[0];
    expect(sent?.body).toEqual({ body: "Проверяю\nсейчас", private: false });
    expect(sent?.headers.get("Idempotency-Key")).toMatch(/^[0-9a-f-]{36}$/);
    const feed = screen.getByRole("log", { name: "Сообщения диалога" });
    expect(await within(feed).findByText(/Проверяю\s+сейчас/)).toBeInTheDocument();
    expect(field).toHaveValue("");
  });

  it("writes an internal note in the yellow mode and keeps the text across the switch", async () => {
    const user = userEvent.setup();
    const { api } = server();
    renderApp("/inbox/c1", { api });
    await user.type(await screen.findByRole("textbox", { name: "Ответ клиенту" }), "Проверь возврат");
    await user.click(screen.getByRole("button", { name: "Заметка", pressed: false }));

    expect(screen.getByText("Клиент не увидит")).toBeInTheDocument();
    const note = screen.getByRole("textbox", { name: "Внутренняя заметка" });
    expect(note).toHaveValue("Проверь возврат");
    await user.click(screen.getByRole("button", { name: "Сохранить заметку" }));
    await waitFor(() =>
      expect(calls(api, "POST", "/conversations/c1/messages")[0]?.body).toEqual({ body: "Проверь возврат", private: true }),
    );
  });

  it("keeps the text after a failed send and retries with the same Idempotency-Key", async () => {
    const user = userEvent.setup();
    let attempts = 0;
    const { api } = server(
      {},
      {
        "POST /conversations/c1/messages": () => {
          attempts += 1;
          return attempts === 1 ? problem(503, "unavailable") : json(201, msg("m9", { body: "Готово", sender_type: "operator", sender_id: me }));
        },
      },
    );
    renderApp("/inbox/c1", { api });
    const field = await screen.findByRole("textbox", { name: "Ответ клиенту" });
    await user.type(field, "Готово{Enter}");
    expect(await screen.findByText("Сообщение не отправлено. Текст сохранён — попробуйте ещё раз.")).toBeInTheDocument();
    expect(field).toHaveValue("Готово");

    await user.click(screen.getByRole("button", { name: "Отправить" }));
    await waitFor(() => expect(field).toHaveValue(""));
    const [first, second] = calls(api, "POST", "/conversations/c1/messages");
    expect(second?.headers.get("Idempotency-Key")).toBe(first?.headers.get("Idempotency-Key"));
  });

  it("pauses the send when a customer message arrived while the operator typed", async () => {
    const user = userEvent.setup();
    const { api, state } = server();
    const { queryClient } = renderApp("/inbox/c1", { api });
    const field = await screen.findByRole("textbox", { name: "Ответ клиенту" });
    await user.type(field, "Сейчас проверю");

    state.feed.push(msg("m2", { body: "Уже не надо, разобрался" }));
    await queryClient.invalidateQueries({ queryKey: ["conversations", "messages", "c1"] });
    await screen.findByText("Уже не надо, разобрался");

    await user.click(screen.getByRole("button", { name: "Отправить" }));
    expect(await screen.findByText("Пока вы писали, в диалоге появилось новое сообщение.")).toBeInTheDocument();
    expect(calls(api, "POST", "/conversations/c1/messages")).toHaveLength(0);
    await user.click(screen.getByRole("button", { name: "Всё равно отправить" }));
    await waitFor(() => expect(calls(api, "POST", "/conversations/c1/messages")).toHaveLength(1));
  });

  it("tells colleagues the operator types, and stops when the reply is sent", async () => {
    const user = userEvent.setup();
    const typing = vi.spyOn(signalSender, "typing");
    const stop = vi.spyOn(signalSender, "stopTyping");
    renderApp("/inbox/c1", { api: server().api });
    await user.type(await screen.findByRole("textbox", { name: "Ответ клиенту" }), "Да{Enter}");
    expect(typing).toHaveBeenCalledWith("c1");
    expect(stop).toHaveBeenCalledWith("c1");
    typing.mockRestore();
    stop.mockRestore();
  });

  it("offers to take a queued conversation before replying", async () => {
    const user = userEvent.setup();
    const { api } = server({ conversation: conv("c1", { status: "queued", assignee_id: null, version: 4 }) });
    renderApp("/inbox/c1", { api });
    expect(await screen.findByText("Диалог в очереди. Возьмите его, чтобы ответить.")).toBeInTheDocument();
    expect(screen.queryByRole("textbox", { name: "Ответ клиенту" })).not.toBeInTheDocument();

    // Two "Взять": the header and the composer; either takes it.
    await user.click(screen.getAllByRole("button", { name: "Взять" })[0] as HTMLElement);
    await waitFor(() =>
      expect(calls(api, "PUT", "/conversations/c1/assignee")[0]?.body).toEqual({ assignee_id: me, version: 4 }),
    );
    expect(await screen.findByRole("textbox", { name: "Ответ клиенту" })).toBeInTheDocument();
  });

  it("leaves only the note to someone else's conversation", async () => {
    const user = userEvent.setup();
    renderApp("/inbox/c1", { api: server({ conversation: conv("c1", { assignee_id: "u-anna" }) }).api });
    expect(
      await screen.findByText("Диалог ведёт Анна Смирнова. Ответить можно после передачи, заметку — сейчас."),
    ).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Написать заметку" }));
    expect(screen.getByRole("textbox", { name: "Внутренняя заметка" })).toBeInTheDocument();
  });
});

describe("conversation actions", () => {
  afterEach(() => resetDrafts());

  it("closes the conversation with the version the operator saw", async () => {
    const user = userEvent.setup();
    const { api } = server({ conversation: conv("c1", { version: 3 }) });
    renderApp("/inbox/c1", { api });
    await user.click(await screen.findByRole("button", { name: "Закрыть" }));
    await waitFor(() =>
      expect(calls(api, "POST", "/conversations/c1/status")[0]?.body).toEqual({ status: "resolved", version: 3 }),
    );
    expect(await within(await conversationRegion()).findByText("Решён")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Переоткрыть" })).toBeInTheDocument();
  });

  it("explains a conflict and shows the fresh state", async () => {
    const user = userEvent.setup();
    const { api, state } = server();
    renderApp("/inbox/c1", { api });
    await screen.findByRole("button", { name: "Закрыть" });
    // A colleague changed it meanwhile.
    state.conversation = { ...state.conversation, status: "pending", version: 2 };
    await user.click(screen.getByRole("button", { name: "Закрыть" }));
    expect(
      await screen.findByText("Диалог успели изменить. Показываем свежую версию — повторите действие."),
    ).toBeInTheDocument();
    expect(await within(await conversationRegion()).findByText("Ждём клиента", { selector: "span" })).toBeInTheDocument();
  });

  it("transfers through the menu and moves the rest under ⋯", async () => {
    const user = userEvent.setup();
    const { api } = server({}, {}, { ...inboxOperator });
    renderApp("/inbox/c1", { api });
    await user.click(await screen.findByRole("button", { name: "Передать" }));
    const menu = screen.getByRole("menu", { name: "Передать" });
    expect(within(menu).getByRole("menuitem", { name: "Анна Смирнова" })).toHaveFocus();
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Передать" })).toHaveFocus();

    await user.click(screen.getByRole("button", { name: "Ещё действия" }));
    expect(screen.getByRole("menuitem", { name: "Ждём клиента" })).toBeInTheDocument();
    expect(screen.getByRole("menuitem", { name: "Вернуть в очередь" })).toBeInTheDocument();
    await user.keyboard("{Escape}");

    await user.click(screen.getByRole("button", { name: "Передать" }));
    await user.click(screen.getByRole("menuitem", { name: "Анна Смирнова" }));
    await waitFor(() =>
      expect(calls(api, "PUT", "/conversations/c1/assignee")[0]?.body).toEqual({ assignee_id: "u-anna", version: 1 }),
    );
    // No longer hers: the actions of the assignee are gone.
    await waitFor(() => expect(screen.queryByRole("button", { name: "Закрыть" })).not.toBeInTheDocument());
  });

  it("hides actions the member may not take", async () => {
    renderApp("/inbox/c1", { api: server({ conversation: conv("c1", { assignee_id: "u-anna" }) }).api });
    await screen.findByRole("heading", { level: 2, name: /Марина Ковалёва\s*№7/ });
    expect(screen.queryByRole("button", { name: "Закрыть" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Передать" })).not.toBeInTheDocument();
  });
});

describe("tags", () => {
  const tags: Tag[] = [
    { id: "t-refund", name: "Возврат", color: "amber" },
    { id: "t-vip", name: "VIP", color: "violet" },
  ];

  it("shows the conversation's tags and adds one from the picker", async () => {
    const user = userEvent.setup();
    const { api } = server({ conversation: conv("c1", { tag_ids: ["t-vip"] }), tags });
    renderApp("/inbox/c1", { api });
    const list = await screen.findByRole("list", { name: "Теги" });
    expect(await within(list).findByText("VIP")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Тег" }));
    const picker = screen.getByRole("dialog", { name: "Теги диалога" });
    expect(within(picker).getByRole("textbox", { name: "Найти тег" })).toHaveFocus();
    await user.type(within(picker).getByRole("textbox"), "воз");
    await user.click(within(picker).getByRole("checkbox", { name: "Возврат" }));
    await waitFor(() =>
      expect(calls(api, "PUT", "/conversations/c1/tags")[0]?.body).toEqual({ tag_ids: ["t-vip", "t-refund"], version: 1 }),
    );
    expect(await within(list).findByText("Возврат")).toBeInTheDocument();
    // Operators cannot create tags (tags.manage).
    expect(within(picker).queryByRole("button", { name: /Создать тег/ })).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Убрать тег «VIP»" }));
    await waitFor(() => expect(calls(api, "PUT", "/conversations/c1/tags")[1]?.body).toEqual({ tag_ids: ["t-refund"], version: 2 }));
  });

  it("lets an admin create a tag from the picker", async () => {
    const user = userEvent.setup();
    const admin = { ...inboxOperator, role: "admin" as const, permissions: [...inboxOperator.permissions, "tags.manage"] };
    const { api } = server(
      { tags },
      {
        "POST /tags": async (request) => {
          const body = (await request.json()) as { name: string };
          return json(201, { id: "t-new", name: body.name, color: "gray" });
        },
      },
      admin,
    );
    renderApp("/inbox/c1", { api });
    await user.click(await screen.findByRole("button", { name: "Тег" }));
    await user.type(screen.getByRole("textbox", { name: "Найти тег" }), "Доставка");
    await user.click(screen.getByRole("button", { name: "Создать тег «Доставка»" }));
    await waitFor(() => expect(calls(api, "PUT", "/conversations/c1/tags")[0]?.body).toEqual({ tag_ids: ["t-new"], version: 1 }));
    expect(calls(api, "POST", "/tags")[0]?.body).toEqual({ name: "Доставка" });
  });
});

describe("feed", () => {
  beforeEach(() => {
    vi.spyOn(document, "visibilityState", "get").mockReturnValue("visible");
  });
  afterEach(() => vi.restoreAllMocks());

  it("renders system lines from events in the reader's language", async () => {
    const feed = [
      msg("m1", { body: "Здравствуйте" }),
      msg("m2", {
        kind: "event",
        sender_type: "system",
        sender_id: me,
        event: { type: "assigned", from: "queued", to: "assigned", assignee_id: me, previous_assignee_id: null },
      }),
      msg("m3", {
        kind: "event",
        sender_type: "system",
        sender_id: me,
        event: { type: "assigned", from: "assigned", to: "assigned", assignee_id: "u-anna", previous_assignee_id: me },
      }),
      msg("m4", {
        kind: "event",
        sender_type: "system",
        sender_id: "u-anna",
        event: { type: "status_changed", from: "assigned", to: "resolved", assignee_id: "u-anna", previous_assignee_id: "u-anna" },
      }),
    ];
    renderApp("/inbox/c1", { api: server({ feed }).api });
    const log = await screen.findByRole("log", { name: "Сообщения диалога" });
    expect(await within(log).findByText(/Взят в работу: Пётр Иванов/)).toBeInTheDocument();
    expect(within(log).getByText(/Передан: Пётр Иванов → Анна Смирнова/)).toBeInTheDocument();
    expect(within(log).getByText(/Решён · Анна Смирнова/)).toBeInTheDocument();
  });

  it("marks the conversation read up to the newest message and divides the new ones", async () => {
    const feed = [msg("m1", { body: "Первое" }), msg("m2", { body: "Второе" }), msg("m3", { body: "Третье" })];
    const { api } = server({ feed, conversation: conv("c1", { last_read_message_id: "m1", unread_count: 2 }) });
    renderApp("/inbox/c1", { api });
    const log = await screen.findByRole("log", { name: "Сообщения диалога" });
    await within(log).findByText("Третье");
    const separator = within(log).getByRole("separator", { name: "Новые сообщения" });
    expect(separator.nextElementSibling).toHaveTextContent("Второе");
    await waitFor(() => expect(calls(api, "POST", "/conversations/c1/read")[0]?.body).toEqual({ up_to_message_id: "m3" }));
  });

  it("does not mark anything read while the tab is hidden", async () => {
    vi.spyOn(document, "visibilityState", "get").mockReturnValue("hidden");
    const { api } = server();
    renderApp("/inbox/c1", { api });
    await screen.findByText("С карты списали два раза");
    expect(calls(api, "POST", "/conversations/c1/read")).toHaveLength(0);
  });
});

describe("list", () => {
  it("shows unread conversations in bold with their counter", async () => {
    const { api } = server({ conversation: conv("c1", { unread_count: 120 }) }, {}, { ...ownerSession, permissions: inboxOperator.permissions });
    renderApp("/inbox", { api });
    const row = await screen.findByRole("link", { name: /Марина Ковалёва/ });
    expect(within(row).getByText("99+")).toBeInTheDocument();
    expect(within(row).getByText("120 непрочитанных")).toHaveClass("sr-only");
  });
});

describe("customer card", () => {
  afterEach(() => {
    window.localStorage.clear();
    useCardSections.setState({ collapsed: ["tech"] });
  });

  const card = () => screen.findByRole("complementary", { name: "Марина Ковалёва" });

  it("shows who the customer is and their other conversations, newest first", async () => {
    const earlier = conv("c0", {
      number: 3,
      status: "closed",
      subject: null,
      created_at: "2025-11-02T10:00:00Z",
      last_message: { sender_type: "contact", kind: "text", text: "Где мой заказ?", attachments: 0, created_at: "2025-11-02T10:00:00Z" },
    });
    const { api } = server({ history: [earlier] });
    renderApp("/inbox/c1", { api });
    const aside = await card();

    expect(await within(aside).findByText("marina@example.com")).toBeInTheDocument();
    expect(within(aside).getByText("Русский")).toBeInTheDocument();
    expect(within(aside).getByText("Клиент с 12 марта 2025 г.")).toBeInTheDocument();
    expect(within(aside).getByText("не указано")).toHaveClass("sr-only");

    const history = await within(aside).findAllByRole("link");
    expect(history).toHaveLength(2);
    expect(history[0]).toHaveAttribute("aria-current", "page");
    expect(history[0]).toHaveTextContent("Этот диалог");
    expect(history[1]).toHaveTextContent(/№3\s*Закрыт/);
    expect(history[1]).toHaveTextContent("Где мой заказ?");
    expect(history[1]).toHaveAttribute("href", "/inbox/c0?tab=mine");
    expect(within(aside).getByLabelText("2 обращения")).toHaveTextContent("2");
  });

  it("says when there are no other conversations", async () => {
    renderApp("/inbox/c1", { api: server().api });
    expect(await within(await card()).findByText("Других обращений нет.")).toBeInTheDocument();
  });

  it("edits the contact, sends only what changed and checks the phone first", async () => {
    const user = userEvent.setup();
    const { api } = server();
    renderApp("/inbox/c1", { api });
    const aside = await card();
    await user.click(await within(aside).findByRole("button", { name: "Изменить" }));

    const phone = within(aside).getByRole("textbox", { name: "Телефон" });
    await user.type(phone, "звоните");
    await user.click(within(aside).getByRole("button", { name: "Сохранить" }));
    expect(await within(aside).findByText(/Цифры, пробелы, скобки и дефис/)).toBeInTheDocument();
    expect(phone).toHaveFocus();
    expect(calls(api, "PATCH", "/contacts/k-c1")).toHaveLength(0);

    await user.clear(phone);
    await user.type(phone, "+7 900 123-45-67");
    await user.selectOptions(within(aside).getByRole("combobox", { name: "Язык" }), "");
    await user.click(within(aside).getByRole("button", { name: "Сохранить" }));

    await waitFor(() => expect(calls(api, "PATCH", "/contacts/k-c1")[0]?.body).toEqual({ phone: "+7 900 123-45-67", locale: "" }));
    expect(await within(aside).findByText("+7 900 123-45-67")).toBeInTheDocument();
    expect(within(aside).queryByRole("textbox", { name: "Телефон" })).not.toBeInTheDocument();
  });

  it("hides editing from members without contacts.edit", async () => {
    const session = { ...inboxOperator, permissions: inboxOperator.permissions.filter((p) => p !== "contacts.edit") };
    renderApp("/inbox/c1", { api: server({}, {}, session).api });
    const aside = await card();
    await within(aside).findByText("marina@example.com");
    expect(within(aside).queryByRole("button", { name: "Изменить" })).not.toBeInTheDocument();
  });

  it("says whether the company signed who the customer is", async () => {
    renderApp("/inbox/c1", { api: server({ contact: contact("k-c1", { identity: "unverified" }) }).api });
    const aside = await card();
    expect(await within(aside).findByText("Личность не подтверждена")).toBeInTheDocument();
  });

  it("opens as a drawer with Ctrl+. and closes with Escape, returning focus", async () => {
    const user = userEvent.setup();
    renderApp("/inbox/c1", { api: server().api });
    const toggle = await screen.findByRole("button", { name: "Карточка клиента (Ctrl+.)" });
    expect(toggle).toHaveAttribute("aria-expanded", "false");

    await user.keyboard("{Control>}.{/Control}");
    expect(toggle).toHaveAttribute("aria-expanded", "true");
    const heading = document.getElementById("customer-card-title");
    await waitFor(() => expect(heading).toHaveFocus());

    await user.keyboard("{Escape}");
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    expect(toggle).toHaveFocus();
  });

  it("remembers folded sections", async () => {
    const user = userEvent.setup();
    renderApp("/inbox/c1", { api: server().api });
    const aside = await card();
    const who = within(aside).getByRole("button", { name: "Кто это" });
    expect(who).toHaveAttribute("aria-expanded", "true");
    expect(within(aside).getByRole("button", { name: "Технические детали" })).toHaveAttribute("aria-expanded", "false");

    await user.click(who);
    expect(who).toHaveAttribute("aria-expanded", "false");
    expect(JSON.parse(window.localStorage.getItem(CARD_SECTIONS_KEY) ?? "[]")).toEqual(["tech", "who"]);
  });
});
