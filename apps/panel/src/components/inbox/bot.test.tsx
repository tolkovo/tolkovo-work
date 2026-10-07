import { screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { fakeApi, json } from "../../test/fake-api";
import { conv, inboxOperator, msg } from "../../test/inbox-fixtures";
import { renderApp } from "../../test/render-app";

// The AI bot in a conversation (ADR-0018): its replies, why it handed over, and taking it from the bot.
describe("the AI bot in the inbox", () => {
  it("names why the bot handed over and offers to take a conversation it answers", async () => {
    const handed = conv("c1", { status: "queued", assignee_id: null });
    const api = fakeApi({
      session: inboxOperator,
      routes: {
        "GET /conversations": () => json(200, { items: [handed], next_cursor: null }),
        "GET /conversations/c1": () => json(200, handed),
        "GET /conversations/c1/messages": () =>
          json(200, {
            items: [
              msg("m3", {
                kind: "event",
                sender_type: "system",
                sender_id: null,
                event: { type: "bot_handoff", from: "bot", to: "queued", assignee_id: null, previous_assignee_id: null, reason: "unknown" },
              }),
              msg("m2", { direction: "out", sender_type: "bot", sender_id: null, body: "Передаю ваш вопрос сотруднику поддержки." }),
              msg("m1", { body: "Как подключить второй аккаунт?" }),
            ],
            next_cursor: null,
          }),
        "GET /contacts/k-c1": () => json(404, {}),
      },
    });
    renderApp("/inbox/c1", { api });
    expect(await screen.findByText(/ИИ передал диалог людям: не знает ответа/)).toBeInTheDocument();
    expect(screen.getByText(/^ИИ-ассистент ·/)).toBeInTheDocument();
  });

  it("lets an operator take a conversation from the bot", async () => {
    const answering = conv("c1", { status: "bot", assignee_id: null });
    const api = fakeApi({
      session: inboxOperator,
      routes: {
        "GET /conversations": () => json(200, { items: [answering], next_cursor: null }),
        "GET /conversations/c1": () => json(200, answering),
        "GET /conversations/c1/messages": () => json(200, { items: [msg("m1", { body: "Привет" })], next_cursor: null }),
        "GET /contacts/k-c1": () => json(404, {}),
      },
    });
    renderApp("/inbox/c1", { api });
    expect((await screen.findAllByRole("button", { name: /^Взять/ })).length).toBeGreaterThan(0);
  });
});
