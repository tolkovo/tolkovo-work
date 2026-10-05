import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { localized, resolveLocale } from "./i18n";
import { FakeServer, FakeSocket, type Call } from "./test/fake";
import type { Options, TokenStorage } from "./types";
import { Widget, applyTheme, isDark } from "./widget";

const KEY = "tk_widget_test";
const BASE = "https://support.example.com";

function memory(): TokenStorage & { data: Map<string, string> } {
  const data = new Map<string, string>();
  return { data, getItem: (k) => data.get(k) ?? null, setItem: (k, v) => void data.set(k, v), removeItem: (k) => void data.delete(k) };
}

/** Lets the widget's promises and Preact's renders run. */
async function settle(): Promise<void> {
  for (let i = 0; i < 10; i++) await new Promise((r) => setTimeout(r, 0));
}

let server: FakeServer;
let widgets: Widget[] = [];

function mount(extra: Partial<Options> = {}, storage = memory()) {
  const w = new Widget({ widgetKey: KEY, apiBase: BASE, locale: "ru", storage, ...extra }, FakeSocket.factory);
  widgets.push(w);
  const root = w.shadowRoot;
  const q = <T extends Element>(sel: string) => root.querySelector<T>(sel);
  return { w, root, q, storage };
}

const calls = (route: string): Call[] => server.calls.filter((c) => `${c.method} ${c.path.split("?")[0]}` === route);

describe("widget", () => {
  beforeEach(() => {
    server = new FakeServer();
    server.install();
    FakeSocket.all = [];
    document.documentElement.lang = "";
  });
  afterEach(() => {
    for (const w of widgets) w.destroy();
    widgets = [];
  });

  it("starts a session, keeps its token and connects to the widget's own realtime", async () => {
    const { q, storage } = mount();
    await settle();
    expect(calls("POST /session")[0]?.body).toEqual({ widget_key: KEY });
    expect(storage.data.get(`tolkovo:${KEY}:session`)).toBe("tk_wsess_1");
    const ws = FakeSocket.last();
    expect(ws.url).toBe("wss://support.example.com/api/v1/widget/realtime");
    expect(ws.protocols).toEqual(["tolkovo.widget.v1", "tk_wsess_1"]);
    expect(q("button.tk-launcher")?.getAttribute("aria-label")).toBe("Открыть чат");
    expect(q(".tk-window")).toBeNull();

    // A second start of the app continues the same session.
    mount({}, storage);
    await settle();
    expect(calls("POST /session")[1]?.body).toEqual({ widget_key: KEY, session_token: "tk_wsess_1" });
  });

  it("sends, shows the reply, and reports it delivered and read", async () => {
    const { q, root } = mount({ greeting: { ru: "Здравствуйте! Чем помочь?", en: "Hi!" }, title: "Зерно" });
    await settle();
    q<HTMLButtonElement>("button.tk-launcher")!.click();
    await settle();
    expect(q(".tk-window")?.getAttribute("aria-label")).toBe("Зерно");
    expect(root.activeElement).toBe(q("textarea"));
    expect(q(".tk-log")?.textContent).toContain("Здравствуйте! Чем помочь?");

    const input = q<HTMLTextAreaElement>("textarea")!;
    input.value = "  Где мой заказ?  ";
    input.dispatchEvent(new Event("input", { bubbles: true }));
    await settle();
    input.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true }));
    await settle();
    const sent = calls("POST /messages")[0]!;
    expect(sent.body).toEqual({ message: "Где мой заказ?" });
    expect(sent.headers["Idempotency-Key"]).toMatch(/.{8,}/);
    expect(input.value).toBe("");
    expect([...root.querySelectorAll(".tk-msg[data-from='contact'] .tk-bubble")].map((b) => b.textContent)).toEqual(["Где мой заказ?"]);
    // The first message gives the session a contact: the connection follows it.
    expect(FakeSocket.all.length).toBe(2);

    const reply = server.reply("Заказ передан в доставку");
    FakeSocket.last().receive("widget.message", { contact_id: "c", conversation_id: "conv-1", message_id: reply.id });
    await settle();
    expect(q(".tk-msg[data-from='operator']:last-of-type .tk-bubble")?.textContent).toBe("Заказ передан в доставку");
    expect(calls("POST /receipts").at(-1)?.body).toEqual({ delivered_up_to: reply.id, read_up_to: reply.id });

    FakeSocket.last().receive("widget.typing", { state: "start" });
    await settle();
    expect(q(".tk-typing")?.textContent).toBe("Оператор печатает…");
  });

  it("counts replies while closed and marks them read on open", async () => {
    const onUnread = vi.fn();
    const { w, q } = mount();
    w.controller.on("unread", onUnread);
    await settle();
    const reply = server.reply("Ответ");
    FakeSocket.last().receive("widget.message");
    await settle();
    expect(q(".tk-badge")?.textContent).toBe("1");
    expect(q("button.tk-launcher")?.getAttribute("aria-label")).toBe("Открыть чат. Новых сообщений: 1");
    expect(onUnread).toHaveBeenLastCalledWith(1);
    expect(calls("POST /receipts").at(-1)?.body).toEqual({ delivered_up_to: reply.id });

    w.controller.setOpen(true);
    await settle();
    expect(q(".tk-badge")).toBeNull();
    expect(onUnread).toHaveBeenLastCalledWith(0);
    expect(calls("POST /receipts").at(-1)?.body).toEqual({ read_up_to: reply.id });

    // Escape closes and the focus goes back to the launcher.
    q(".tk-window")!.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    await settle();
    expect(q(".tk-window")).toBeNull();
    expect(w.shadowRoot.activeElement).toBe(q("button.tk-launcher"));
  });

  it("starts a new session when the server ended it, and keeps a failed message to retry", async () => {
    const { w, q } = mount({ open: true });
    await settle();
    server.tokens.clear(); // the session expired on the server
    await w.controller.send("Алло");
    await settle();
    expect(calls("POST /session")).toHaveLength(2);
    expect(calls("POST /messages")).toHaveLength(2);
    expect(q(".tk-msg[data-from='contact'] .tk-bubble")?.textContent).toBe("Алло");

    server.failNext.push({ route: "POST /messages", status: 503, code: "dependency_unavailable" });
    await w.controller.send("Ещё раз");
    await settle();
    expect(q(".tk-failed")?.textContent).toContain("Не отправлено.");
    const key = calls("POST /messages").at(-1)!.headers["Idempotency-Key"];
    q<HTMLButtonElement>(".tk-failed button")!.click();
    await settle();
    expect(calls("POST /messages").at(-1)!.headers["Idempotency-Key"]).toBe(key);
    expect(q(".tk-failed")).toBeNull();
  });

  it("identifies a signed-in user and ends the session on logout", async () => {
    const { w, storage } = mount();
    await settle();
    await w.controller.identify({ id: "u-42", hash: "ab".repeat(32), name: "Марина" });
    expect(calls("POST /session").at(-1)?.body).toEqual({
      widget_key: KEY,
      session_token: "tk_wsess_1",
      user: { external_id: "u-42", user_hash: "ab".repeat(32), name: "Марина" },
    });
    await w.controller.logout();
    expect(calls("DELETE /session")[0]?.headers.Authorization).toBe("Bearer tk_wsess_1");
    // A new anonymous session follows at once.
    expect(storage.data.get(`tolkovo:${KEY}:session`)).toBe("tk_wsess_2");
    expect(calls("POST /session").at(-1)?.body).toEqual({ widget_key: KEY });
  });

  it("says when the server refuses the widget", async () => {
    server.failNext.push({ route: "POST /session", status: 403, code: "origin_not_allowed" });
    const { q } = mount({ open: true });
    await settle();
    expect(q(".tk-notice")?.textContent).toBe("Чат сейчас недоступен. Попробуйте позже.");
    expect(q<HTMLTextAreaElement>("textarea")?.disabled).toBe(true);
  });

  it("applies the theme from code and hides the launcher on request", async () => {
    const { w, q } = mount({ theme: { accent: "#1d4ed8", radius: 6, position: "bottom-left", mode: "dark" }, launcher: false });
    await settle();
    const host = document.querySelector("tolkovo-widget") as HTMLElement;
    expect(host.dataset.mode).toBe("dark");
    expect(host.style.getPropertyValue("--tk-accent")).toBe("#1d4ed8");
    expect(host.style.getPropertyValue("--tk-on-accent")).toBe("var(--tk-on-dark)");
    expect(host.style.getPropertyValue("--tk-radius")).toBe("6px");
    expect(q(".tk-root")?.getAttribute("data-position")).toBe("bottom-left");
    expect(q("button.tk-launcher")).toBeNull();
    w.controller.setLauncher(true);
    await settle();
    expect(q("button.tk-launcher")).not.toBeNull();
  });
});

describe("theme", () => {
  it("picks the text color for contrast and ignores values the browser refuses", () => {
    expect(isDark("#0d7a69")).toBe(true);
    expect(isDark("#ffd400")).toBe(false);
    expect(isDark("#fff")).toBe(false);
    expect(isDark("rebeccapurple")).toBeNull();
    const el = document.createElement("div");
    vi.stubGlobal("CSS", { supports: (_p: string, v: string) => !v.includes(";") });
    applyTheme(el, { accent: "red; background: url(x)", radius: 9999, offsetX: 12, fontFamily: "Inter, sans-serif" });
    expect(el.style.getPropertyValue("--tk-accent")).toBe("");
    expect(el.style.getPropertyValue("--tk-radius")).toBe("");
    expect(el.style.getPropertyValue("--tk-offset-x")).toBe("12px");
    expect(el.style.getPropertyValue("--tk-font")).toBe("Inter, sans-serif");
  });
});

describe("locale", () => {
  it("takes the option, then the page's language, then the browser's", () => {
    expect(resolveLocale("en")).toBe("en");
    document.documentElement.lang = "ru-RU";
    expect(resolveLocale("auto")).toBe("ru");
    document.documentElement.lang = "de";
    expect(["ru", "en"]).toContain(resolveLocale(undefined));
    expect(localized({ ru: "Привет" }, "en")).toBe("Привет");
    expect(localized("Hello", "ru")).toBe("Hello");
  });
});
