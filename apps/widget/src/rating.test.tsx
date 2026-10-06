import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { pagePath, report } from "./analytics";
import { FakeServer, FakeSocket, type Call } from "./test/fake";
import type { Events, Options, TokenStorage } from "./types";
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

function type(el: HTMLInputElement | HTMLTextAreaElement, value: string) {
  el.value = value;
  el.dispatchEvent(new Event("input", { bubbles: true }));
}

async function sendMessage(q: <T extends Element>(sel: string) => T | null, text: string) {
  type(q<HTMLTextAreaElement>("#tk-input")!, text);
  await settle();
  q<HTMLFormElement>(".tk-composer")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
  await settle();
}

/** The operator resolved the conversation: the server tells the widget. */
async function resolve() {
  server.resolved = true;
  FakeSocket.last().receive("widget.conversation", { contact_id: "c", conversation_id: server.conversationId });
  await settle();
}

describe("rating", () => {
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

  it("asks for a rating once the conversation is resolved, with an optional comment", async () => {
    const { w, q, root } = mount();
    const rated: Events["rated"][] = [];
    w.controller.on("rated", (p) => rated.push(p));
    await settle();
    await sendMessage(q, "Где мой заказ?");
    server.reply("Заказ в пути");
    expect(q(".tk-rate")).toBeNull();

    await resolve();
    expect(q(".tk-rate-title")?.textContent).toBe("Оцените, как мы помогли");
    const stars = Array.from(root.querySelectorAll<HTMLButtonElement>(".tk-stars button"));
    expect(stars.map((s) => s.getAttribute("aria-label"))).toEqual(["1 из 5", "2 из 5", "3 из 5", "4 из 5", "5 из 5"]);
    expect(q("#tk-rate-comment")).toBeNull();

    stars[3]!.click();
    await settle();
    expect(stars[3]!.getAttribute("aria-checked")).toBe("true");
    expect(root.querySelectorAll(".tk-stars [data-on]")).toHaveLength(4);
    type(q<HTMLTextAreaElement>("#tk-rate-comment")!, "  Быстро  ");
    await settle();
    q<HTMLFormElement>(".tk-rate-form")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    await settle();

    expect(calls("POST /rating")[0]?.body).toEqual({ conversation_id: "conv-1", score: 4, comment: "Быстро" });
    expect(q(".tk-rate-title")?.textContent).toBe("Спасибо за оценку!");
    expect(q(".tk-rate [role=img]")?.getAttribute("aria-label")).toBe("Ваша оценка: 4 из 5");
    expect(rated).toEqual([{ score: 4, resolvedBy: "human" }]);

    // Writing again reopens the conversation: the card goes.
    await sendMessage(q, "И ещё вопрос");
    expect(q(".tk-rate")).toBeNull();
  });

  it("moves between the stars with the arrows", async () => {
    const { q, root } = mount();
    await settle();
    await sendMessage(q, "Вопрос");
    await resolve();
    const stars = () => Array.from(root.querySelectorAll<HTMLButtonElement>(".tk-stars button"));
    expect(stars().map((s) => s.tabIndex)).toEqual([0, -1, -1, -1, -1]);
    q(".tk-stars")!.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowRight", bubbles: true }));
    await settle();
    q(".tk-stars")!.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowRight", bubbles: true }));
    await settle();
    expect(stars()[1]!.getAttribute("aria-checked")).toBe("true");
    expect(root.activeElement).toBe(stars()[1]);
  });

  it("explains a failure and stops asking when the conversation reopened meanwhile", async () => {
    const { q } = mount();
    await settle();
    await sendMessage(q, "Вопрос");
    await resolve();
    q<HTMLButtonElement>(".tk-stars button")!.click();
    await settle();

    server.failNext.push({ route: "POST /rating", status: 503, code: "dependency_unavailable" });
    q<HTMLFormElement>(".tk-rate-form")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    await settle();
    expect(q(".tk-rate [role=alert]")?.textContent).toBe("Не удалось отправить. Повторите.");

    server.resolved = false; // the customer wrote from another tab
    q<HTMLFormElement>(".tk-rate-form")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    await settle();
    expect(q(".tk-rate")).toBeNull();
  });

  it("shows the rating already given", async () => {
    server.reply("Здравствуйте");
    server.resolved = true;
    server.rating = { score: 5, comment: null, resolved_by: "human", rated_at: "2026-10-06T10:00:00Z" };
    const { q } = mount();
    await settle();
    expect(q(".tk-rate [role=img]")?.getAttribute("aria-label")).toBe("Ваша оценка: 5 из 5");
  });
});

describe("goals in the site's analytics", () => {
  let dataLayer: unknown[];
  let gtag: ReturnType<typeof vi.fn>;
  let ym: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    server = new FakeServer();
    server.install();
    FakeSocket.all = [];
    dataLayer = [];
    gtag = vi.fn();
    ym = vi.fn();
    vi.stubGlobal("dataLayer", dataLayer);
    vi.stubGlobal("gtag", gtag);
    vi.stubGlobal("ym", ym);
  });
  afterEach(() => {
    for (const w of widgets) w.destroy();
    widgets = [];
    vi.unstubAllGlobals();
  });

  it("reports the goals to every counter turned on, without personal data", async () => {
    server.analytics = { data_layer: true, gtag: true, metrika_counter: "12345678", goals: { chat_started: "chat", contact_collected: "lead", rated: "csat" } };
    const { w, q } = mount({ prechat: { fields: ["name", "email"] } });
    const started: Events["chatStarted"][] = [];
    w.controller.on("chatStarted", (p) => started.push(p));
    await settle();

    type(q<HTMLInputElement>("#tk-pre-name")!, "Анна Петрова");
    type(q<HTMLInputElement>("#tk-pre-email")!, "anna@example.com");
    await settle();
    q<HTMLFormElement>(".tk-prechat")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    await settle();
    await sendMessage(q, "Мой телефон +7 900 000-00-00");
    await sendMessage(q, "И ещё");
    await resolve();
    q<HTMLButtonElement>(".tk-stars button:nth-child(5)")!.click();
    await settle();
    q<HTMLFormElement>(".tk-rate-form")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    await settle();

    const page = location.pathname;
    expect(dataLayer).toEqual([
      { event: "lead", tolkovo: { source: "prechat" } },
      { event: "chat", tolkovo: { channel: "widget", page } },
      { event: "csat", tolkovo: { score: 5, resolvedBy: "human" } },
    ]);
    expect(gtag.mock.calls).toEqual([
      ["event", "lead", { source: "prechat" }],
      ["event", "chat", { channel: "widget", page }],
      ["event", "csat", { score: 5, resolvedBy: "human" }],
    ]);
    expect(ym.mock.calls).toEqual([
      [12345678, "reachGoal", "lead", { source: "prechat" }],
      [12345678, "reachGoal", "chat", { channel: "widget", page }],
      [12345678, "reachGoal", "csat", { score: 5, resolvedBy: "human" }],
    ]);
    // One chat, one goal; the same parameters reach the site's own handlers.
    expect(started).toEqual([{ channel: "widget", page }]);
    const everything = JSON.stringify([dataLayer, gtag.mock.calls, ym.mock.calls]);
    for (const secret of ["Анна", "anna@", "+7 900", "Мой телефон", "tk_wsess", "conv-1"]) expect(everything).not.toContain(secret);
  });

  it("reports nothing until the panel turns a counter on", async () => {
    const { q } = mount();
    await settle();
    await sendMessage(q, "Вопрос");
    expect(dataLayer).toEqual([]);
    expect(gtag).not.toHaveBeenCalled();
    expect(ym).not.toHaveBeenCalled();
  });

  it("starts a new chat goal for the conversation after a closed one", async () => {
    server.analytics = { ...server.analytics, data_layer: true };
    const { q } = mount();
    await settle();
    await sendMessage(q, "Первый");
    server.conversationId = "conv-2"; // the first was closed: the next message opens another
    await sendMessage(q, "Второй");
    expect(dataLayer.map((e) => (e as { event: string }).event)).toEqual(["tolkovo.chatStarted", "tolkovo.chatStarted"]);
  });

  it("skips missing counters and survives one that throws", () => {
    const a = { data_layer: true, gtag: true, metrika_counter: "1", goals: { chat_started: "c", contact_collected: "l", rated: "r" } };
    expect(() => report(a, "rated", { score: 1 }, {})).not.toThrow();
    const layer: unknown[] = [];
    const broken = () => {
      throw new Error("counter is broken");
    };
    report(a, "rated", { score: 1 }, { dataLayer: layer, gtag: broken, ym: broken });
    expect(layer).toEqual([{ event: "r", tolkovo: { score: 1 } }]);
  });

  it("keeps the page path only, and none for a local file", () => {
    expect(pagePath({ protocol: "https:", pathname: "/checkout" })).toBe("/checkout");
    expect(pagePath({ protocol: "file:", pathname: "/Users/anna/app/index.html" })).toBe("");
  });
});
