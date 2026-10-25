import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { FakeServer, FakeSocket } from "./test/fake";
import type { Options, TokenStorage } from "./types";
import { Widget } from "./widget";

const KEY = "tk_widget_test";
const BASE = "https://support.example.com";

function memory(): TokenStorage {
  const data = new Map<string, string>();
  return { getItem: (k) => data.get(k) ?? null, setItem: (k, v) => void data.set(k, v), removeItem: (k) => void data.delete(k) };
}

async function settle(): Promise<void> {
  for (let i = 0; i < 10; i++) await new Promise((r) => setTimeout(r, 0));
}

let server: FakeServer;
let widgets: Widget[] = [];

function mount(extra: Partial<Options> = {}) {
  const w = new Widget({ widgetKey: KEY, apiBase: BASE, locale: "ru", storage: memory(), open: true, ...extra }, FakeSocket.factory);
  widgets.push(w);
  const root = w.shadowRoot;
  const callButton = () => Array.from(root.querySelectorAll<HTMLButtonElement>("button")).find((b) => b.textContent === "Позвать оператора");
  return { w, root, callButton };
}

// A new message of the conversation: the widget refetches the chat.
async function push() {
  FakeSocket.last().receive("widget.message", { contact_id: "c", conversation_id: server.conversationId, message_id: "m" });
  await settle();
}

// While the AI bot answers, the visitor can call a person (AI-05).
describe("the AI bot", () => {
  beforeEach(() => {
    server = new FakeServer();
    server.install();
    FakeSocket.all = [];
  });
  afterEach(() => {
    for (const w of widgets) w.destroy();
    widgets = [];
    vi.unstubAllGlobals();
  });

  it("offers to call a person while the bot answers, and sends the request as a message", async () => {
    server.withBot = true;
    server.reply("Здравствуйте! Я ИИ-ассистент.", "bot");
    const { callButton } = mount();
    await settle();
    const button = callButton();
    expect(button).toBeDefined();

    button!.click();
    await settle();
    expect(server.calls.filter((c) => `${c.method} ${c.path.split("?")[0]}` === "POST /messages").map((c) => c.body)).toEqual([
      { message: "Позвать оператора" },
    ]);
    expect(callButton()).toBeUndefined();

    // The bot handed over: no button any more.
    server.withBot = false;
    server.reply("Передаю ваш вопрос сотруднику поддержки.", "bot");
    await push();
    expect(callButton()).toBeUndefined();
  });

  it("offers nothing when people answer", async () => {
    server.reply("Здравствуйте!");
    const { callButton } = mount();
    await settle();
    expect(callButton()).toBeUndefined();
  });
});
