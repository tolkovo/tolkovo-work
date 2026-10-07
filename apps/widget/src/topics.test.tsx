import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { FakeServer, FakeSocket, type Call } from "./test/fake";
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
  return { w, root, q: <T extends Element>(sel: string) => root.querySelector<T>(sel) };
}

const calls = (route: string): Call[] => server.calls.filter((c) => `${c.method} ${c.path.split("?")[0]}` === route);

async function sendMessage(q: <T extends Element>(sel: string) => T | null, text: string) {
  const input = q<HTMLTextAreaElement>("#tk-input")!;
  input.value = text;
  input.dispatchEvent(new Event("input", { bubbles: true }));
  await settle();
  q<HTMLFormElement>(".tk-composer")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
  await settle();
}

const topicButtons = (root: ShadowRoot) => Array.from(root.querySelectorAll<HTMLButtonElement>(".tk-topic"));

describe("topics", () => {
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

  it("asks nothing without topics", async () => {
    const { q } = mount();
    await settle();
    expect(q(".tk-topics")).toBeNull();
  });

  it("sends the topic picked before writing with the first message, then hides the choice", async () => {
    server.topics = [
      { id: "t-crm", name: "CRM" },
      { id: "t-of", name: "OnlyFans" },
    ];
    const { q, root } = mount();
    await settle();
    expect(q(".tk-topics")?.getAttribute("aria-label")).toBe("Тема вопроса");
    expect(topicButtons(root).map((b) => b.textContent)).toEqual(["CRM", "OnlyFans"]);

    topicButtons(root)[1]!.click();
    await settle();
    expect(topicButtons(root)[1]!.getAttribute("aria-pressed")).toBe("true");
    // Pressed again, the choice is taken back.
    topicButtons(root)[1]!.click();
    await settle();
    expect(topicButtons(root)[1]!.getAttribute("aria-pressed")).toBe("false");
    topicButtons(root)[0]!.click();
    await settle();

    await sendMessage(q, "Не открывается CRM");
    expect(calls("POST /messages")[0]?.body).toEqual({ message: "Не открывается CRM", topic_id: "t-crm" });
    expect(q(".tk-topics")).toBeNull();
    // The next message carries no topic: the conversation has one.
    await sendMessage(q, "ещё");
    expect(calls("POST /messages")[1]?.body).toEqual({ message: "ещё" });
  });

  it("picks the topic of an open conversation that has none at once", async () => {
    server.topics = [{ id: "t-crm", name: "CRM" }];
    const { q, root } = mount();
    await settle();
    await sendMessage(q, "Здравствуйте");
    expect(calls("POST /messages")[0]?.body).toEqual({ message: "Здравствуйте" });
    expect(q(".tk-topics")).not.toBeNull();

    topicButtons(root)[0]!.click();
    await settle();
    expect(calls("POST /topic")[0]?.body).toEqual({ conversation_id: "conv-1", topic_id: "t-crm" });
    expect(q(".tk-topics")).toBeNull();
  });
});
