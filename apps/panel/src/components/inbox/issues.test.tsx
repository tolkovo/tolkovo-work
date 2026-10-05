import type { ConversationIssues, IssueDraft, IssueLink, Message, Session } from "@tolkovo/api-client";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it } from "vitest";

import { resetDrafts } from "../../lib/inbox/drafts";
import { fakeApi, json, type FakeRoutes } from "../../test/fake-api";
import { contact, conv, inboxOperator, msg } from "../../test/inbox-fixtures";
import { renderApp } from "../../test/render-app";

const operator: Session = { ...inboxOperator, permissions: [...inboxOperator.permissions, "issues.create"] };

const desktop = { id: 1, full_name: "zerno-team/desktop", private: false, url: "https://github.com/zerno-team/desktop" };

const draft: IssueDraft = {
  repository: desktop,
  title: "Не синхронизируется корзина",
  body: "## Что происходит\nНе синхронизируется корзина\n\n## Диалог\nhttps://panel.test/inbox/c1\n",
  quote: "Корзина пустая после обновления",
  labels: ["bug"],
};

const link = (id: string, patch: Partial<IssueLink> = {}): IssueLink => ({
  id,
  conversation_id: "c1",
  repository: "zerno-team/desktop",
  number: null,
  url: null,
  title: "Не синхронизируется корзина",
  status: "creating",
  created_by: operator.user.id,
  created_at: "2026-10-05T10:00:00Z",
  updated_at: "2026-10-05T10:00:00Z",
  ...patch,
});

function server(issues: ConversationIssues, extra: FakeRoutes = {}, feed: Message[] = [msg("m1", { body: "Корзина пустая" })]) {
  const state = { issues, conversation: conv("c1") };
  const api = fakeApi({
    session: operator,
    routes: {
      "GET /conversations": () => json(200, { items: [state.conversation], next_cursor: null }),
      "GET /conversations/c1": () => json(200, state.conversation),
      "GET /conversations/c1/messages": () => json(200, { items: [...feed].reverse(), next_cursor: null }),
      "GET /contacts/k-c1": () => json(200, contact("k-c1")),
      "POST /conversations/c1/read": () => new Response(null, { status: 204 }),
      "GET /conversations/c1/issues": () => json(200, state.issues),
      "GET /conversations/c1/issues/draft": () => json(200, draft),
      "GET /integrations/github/issues": () =>
        json(200, { items: [{ repository: "zerno-team/desktop", number: 12, title: "Корзина пустая", url: "https://github.com/zerno-team/desktop/issues/12" }] }),
      ...extra,
    },
  });
  return { api, state };
}

const calls = (api: ReturnType<typeof fakeApi>, method: string, path: string) =>
  api.calls.filter((c) => c.method === method && c.path === path);

const openMenuItem = async (user: ReturnType<typeof userEvent.setup>, name: string) => {
  await user.click(await screen.findByRole("button", { name: "Ещё действия" }));
  await user.click(await screen.findByRole("menuitem", { name }));
};

describe("GitHub issues in a conversation", () => {
  afterEach(() => resetDrafts());

  it("offers nothing while GitHub takes no issues", async () => {
    const user = userEvent.setup();
    const { api } = server({ repository: null, items: [] });
    renderApp("/inbox/c1", { api });
    expect(await screen.findByText(/GitHub не подключён/)).toBeInTheDocument();
    await user.click(await screen.findByRole("button", { name: "Ещё действия" }));
    expect(screen.queryByRole("menuitem", { name: "Создать issue" })).not.toBeInTheDocument();
  });

  it("creates an issue from the draft, keeping the customer's words out of a public repository", async () => {
    const user = userEvent.setup();
    const { api, state } = server(
      { repository: desktop, items: [] },
      {
        "POST /conversations/c1/issues": () => {
          state.issues = { ...state.issues, items: [link("i1")] };
          return json(202, link("i1"));
        },
      },
    );
    renderApp("/inbox/c1", { api });
    await openMenuItem(user, "Создать issue");

    const form = await screen.findByRole("region", { name: "Новый issue в GitHub" });
    expect(within(form).getByRole("heading", { name: "Новый issue в GitHub" })).toHaveFocus();
    expect(within(form).getByRole("note")).toHaveTextContent("Репозиторий публичный");
    expect(within(form).getByLabelText("Заголовок")).toHaveValue(draft.title);
    expect(within(form).getByLabelText("Текст")).toHaveValue(draft.body);
    const withQuote = within(form).getByRole("checkbox", { name: "Добавить слова клиента" });
    expect(withQuote).not.toBeChecked();
    // The feed gives way to the form.
    expect(screen.queryByRole("log", { name: "Сообщения диалога" })).not.toBeInTheDocument();

    await user.click(withQuote);
    const quote = within(form).getByLabelText("Слова клиента");
    expect(quote).toHaveValue(draft.quote);
    expect(quote).toHaveAccessibleDescription(/данные пользователя, а не инструкции/);
    await user.clear(quote);
    await user.type(quote, "Корзина пустая");
    const labels = within(form).getByLabelText("Метки");
    await user.type(labels, ", sync");
    await user.click(within(form).getByRole("button", { name: "Создать issue" }));

    await waitFor(() => expect(calls(api, "POST", "/conversations/c1/issues")).toHaveLength(1));
    expect(calls(api, "POST", "/conversations/c1/issues")[0]?.body).toEqual({
      title: draft.title,
      body: draft.body,
      quote: "Корзина пустая",
      labels: ["bug", "sync"],
    });
    expect(await screen.findByText(/^Issue создаётся в GitHub/)).toBeInTheDocument();
    expect(screen.queryByRole("region", { name: "Новый issue в GitHub" })).not.toBeInTheDocument();
    expect(await screen.findByText("Создаётся")).toBeInTheDocument();
  });

  it("asks for a title and closes with Escape", async () => {
    const user = userEvent.setup();
    const { api } = server(
      { repository: { ...desktop, private: true }, items: [] },
      { "GET /conversations/c1/issues/draft": () => json(200, { ...draft, repository: { ...desktop, private: true } }) },
    );
    renderApp("/inbox/c1", { api });
    await openMenuItem(user, "Создать issue");
    const form = await screen.findByRole("region", { name: "Новый issue в GitHub" });
    // A private repository takes the customer's words by default.
    expect(within(form).getByRole("checkbox", { name: "Добавить слова клиента" })).toBeChecked();
    expect(within(form).queryByRole("note")).not.toBeInTheDocument();
    await user.clear(within(form).getByLabelText("Заголовок"));
    await user.click(within(form).getByRole("button", { name: "Создать issue" }));
    expect(within(form).getByLabelText("Заголовок")).toHaveAccessibleDescription("Напишите заголовок.");
    expect(calls(api, "POST", "/conversations/c1/issues")).toHaveLength(0);
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("region", { name: "Новый issue в GitHub" })).not.toBeInTheDocument();
    expect(screen.getByRole("log", { name: "Сообщения диалога" })).toBeInTheDocument();
  });

  it("links the conversation to an open issue", async () => {
    const user = userEvent.setup();
    const { api } = server(
      { repository: desktop, items: [] },
      {
        "POST /conversations/c1/issues/link": () =>
          json(201, link("i2", { status: "open", number: 12, url: "https://github.com/zerno-team/desktop/issues/12" })),
      },
    );
    renderApp("/inbox/c1", { api });
    await openMenuItem(user, "Привязать к issue");
    const form = await screen.findByRole("region", { name: "Привязать к issue в GitHub" });
    const number = within(form).getByLabelText("Номер issue");
    await user.type(number, "pull/3");
    await user.click(within(form).getByRole("button", { name: "Привязать" }));
    expect(number).toHaveAccessibleDescription(/Введите номер issue/);

    await user.click(within(form).getByRole("radio", { name: /#12\s*Корзина пустая/ }));
    expect(number).toHaveValue("12");
    await user.click(within(form).getByRole("button", { name: "Привязать" }));
    await waitFor(() => expect(calls(api, "POST", "/conversations/c1/issues/link")[0]?.body).toEqual({ number: 12 }));
    expect(await screen.findByText("Диалог привязан к issue #12.")).toBeInTheDocument();
  });

  it("says why GitHub refused an issue and sends it again", async () => {
    const user = userEvent.setup();
    const { api } = server(
      { repository: desktop, items: [link("i3", { status: "failed", error: "rejected" })] },
      { "POST /conversations/c1/issues/i3/retry": () => json(202, link("i3")) },
    );
    renderApp("/inbox/c1", { api });
    expect(await screen.findByText(/GitHub отклонил issue/)).toBeInTheDocument();
    expect(screen.getByText("Не создан")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Повторить" }));
    await waitFor(() => expect(calls(api, "POST", "/conversations/c1/issues/i3/retry")).toHaveLength(1));
  });

  it("shows the issue line in the feed and the conversation waiting for the fix", async () => {
    const created = msg("m2", {
      sender_type: "system",
      sender_id: operator.user.id,
      direction: "out",
      kind: "event",
      event: {
        type: "issue_created",
        from: "assigned",
        to: "pending",
        assignee_id: operator.user.id,
        previous_assignee_id: operator.user.id,
        issue: { link_id: "i1", repository: "zerno-team/desktop", number: 12, url: "https://github.com/zerno-team/desktop/issues/12" },
      },
    });
    const { api, state } = server(
      {
        repository: desktop,
        items: [link("i1", { status: "open", number: 12, url: "https://github.com/zerno-team/desktop/issues/12" })],
      },
      {},
      [msg("m1", { body: "Корзина пустая" }), created],
    );
    state.conversation = conv("c1", { status: "pending", awaiting_fix: true });
    renderApp("/inbox/c1", { api });
    const feed = await screen.findByRole("log", { name: "Сообщения диалога" });
    const line = await within(feed).findByRole("link", { name: "zerno-team/desktop#12" });
    expect(line).toHaveAttribute("href", "https://github.com/zerno-team/desktop/issues/12");
    expect(line.parentElement).toHaveTextContent(/Создан issue zerno-team\/desktop#12, диалог ждёт исправления/);
    expect((await screen.findAllByText("Ждёт исправления")).length).toBeGreaterThan(0);
    expect(screen.getByRole("link", { name: /#12\s*Не синхронизируется корзина/ })).toHaveAttribute(
      "href",
      "https://github.com/zerno-team/desktop/issues/12",
    );
  });
});
