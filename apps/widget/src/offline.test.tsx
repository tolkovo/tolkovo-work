import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { FakeServer, FakeSocket } from "./test/fake";
import type { Options, TokenStorage } from "./types";
import { Widget, offlineView } from "./widget";

const KEY = "tk_widget_test";
const POLICY = "https://shop.example.com/privacy";

function memory(): TokenStorage {
  const data = new Map<string, string>();
  return { getItem: (k) => data.get(k) ?? null, setItem: (k, v) => void data.set(k, v), removeItem: (k) => void data.delete(k) };
}

async function settle(): Promise<void> {
  for (let i = 0; i < 10; i++) await new Promise((r) => setTimeout(r, 0));
}

let server: FakeServer;
let widgets: Widget[] = [];

function mount(extra: Partial<Options> = {}, storage = memory()) {
  const w = new Widget({ widgetKey: KEY, apiBase: "https://support.example.com", locale: "ru", storage, open: true, ...extra }, FakeSocket.factory);
  widgets.push(w);
  const q = <T extends Element>(sel: string) => w.shadowRoot.querySelector<T>(sel);
  return { w, q, storage };
}

async function type(input: HTMLInputElement | HTMLTextAreaElement | null, value: string) {
  input!.value = value;
  input!.dispatchEvent(new Event("input", { bubbles: true }));
  await settle();
}

const submit = (q: ReturnType<typeof mount>["q"]) => q<HTMLButtonElement>("button.tk-start")!.click();
const calls = (route: string) => server.calls.filter((c) => `${c.method} ${c.path}` === route);

describe("offline form", () => {
  beforeEach(() => {
    server = new FakeServer();
    server.install();
    server.operatorsOnline = false;
    FakeSocket.all = [];
  });
  afterEach(() => {
    for (const w of widgets) w.destroy();
    widgets = [];
    vi.useRealTimers();
  });

  it("takes a message and a way to reach the visitor while nobody is there", async () => {
    const { w, q } = mount({ prechat: { consent: { policyUrl: POLICY } } });
    const collected: unknown[] = [];
    w.controller.on("contactCollected", (p) => collected.push(p));
    await settle();
    // The offline form, not the pre-chat one or the message field.
    expect(q(".tk-prechat-intro")?.textContent).toContain("Сейчас никого нет на месте");
    expect(q(".tk-composer")).toBeNull();
    expect([...w.shadowRoot.querySelectorAll(".tk-field > label:not(.tk-consent)")].map((l) => l.textContent)).toEqual(["Имя", "Почта *", "Сообщение *"]);
    // The pre-chat consent applies here too.
    expect(q<HTMLAnchorElement>(".tk-consent a")?.href).toBe(POLICY);

    await type(q("#tk-pre-name"), "Анна");
    submit(q);
    await settle();
    expect(q("#tk-pre-email-error")?.textContent).toBe("Заполните поле.");
    expect(q("#tk-pre-message-error")?.textContent).toBe("Заполните поле.");
    expect(q("#tk-pre-consent-error")?.textContent).toBe("Без согласия сообщение не отправить.");
    expect(w.shadowRoot.activeElement?.id).toBe("tk-pre-email");
    expect(calls("POST /contact")).toHaveLength(0);

    await type(q("#tk-pre-email"), "anna@example.com");
    await type(q("#tk-pre-message"), "  Перезвоните завтра ");
    q<HTMLInputElement>("#tk-pre-consent")!.click();
    await settle();
    submit(q);
    await settle();
    expect(calls("POST /contact").map((c) => c.body)).toEqual([
      { name: "Анна", email: "anna@example.com", consent: { policy_url: POLICY }, form: "offline" },
    ]);
    expect(calls("POST /messages").map((c) => c.body)).toEqual([{ message: "Перезвоните завтра" }]);
    expect(collected).toEqual([{ source: "offline" }]);
    // The chat with the message, and what to expect.
    expect(q(".tk-prechat")).toBeNull();
    expect(q(".tk-msg[data-from=contact] .tk-bubble")?.textContent).toBe("Перезвоните завтра");
    expect(q(".tk-notice[data-kind=away]")?.textContent).toBe("Сейчас никого нет на месте. Ответим, как только вернёмся.");
    expect(q("textarea.tk-input")).not.toBeNull();
  });

  it("is not shown while someone is there, to a visitor who wrote, or when turned off", async () => {
    server.operatorsOnline = true;
    const online = mount();
    await settle();
    expect(online.q(".tk-prechat")).toBeNull();
    expect(online.q(".tk-notice")).toBeNull();
    online.w.destroy();
    widgets = [];

    server.operatorsOnline = false;
    server.reply("Здравствуйте!");
    server.messages.push({ id: "0199f000-0000-7000-8000-000000000000", conversation_id: "conv-1", sender: "contact", body: "Вопрос", created_at: "2026-10-05T09:59:00Z", files: [] });
    const wrote = mount();
    await settle();
    expect(wrote.q(".tk-prechat")).toBeNull();
    expect(wrote.q(".tk-notice[data-kind=away]")).not.toBeNull();
    wrote.w.destroy();
    widgets = [];

    server.messages = [];
    const off = mount({ offline: false });
    await settle();
    expect(off.q(".tk-prechat")).toBeNull();
    expect(off.q("textarea.tk-input")).not.toBeNull();
  });

  it("asks again on opening the window a while later", async () => {
    vi.useFakeTimers({ toFake: ["Date"] });
    server.operatorsOnline = true;
    const { w, q } = mount({ open: false });
    await settle();
    w.controller.setOpen(true);
    await settle();
    // Just asked at the start.
    expect(calls("GET /availability")).toHaveLength(0);
    expect(q(".tk-prechat")).toBeNull();

    w.controller.setOpen(false);
    server.operatorsOnline = false;
    vi.setSystemTime(Date.now() + 60_000);
    w.controller.setOpen(true);
    await settle();
    expect(calls("GET /availability")).toHaveLength(1);
    expect(q(".tk-prechat-intro")?.textContent).toContain("никого нет");
  });

  it("of the email and the phone, takes either", async () => {
    const { q } = mount({ offline: { fields: ["email", "phone"] } });
    await settle();
    await type(q("#tk-pre-message"), "Позвоните");
    submit(q);
    await settle();
    expect(q("#tk-pre-email-error")?.textContent).toBe("Оставьте почту или телефон, чтобы мы могли ответить.");
    expect(q("#tk-pre-phone-error")).toBeNull();
    await type(q("#tk-pre-phone"), "+7 900 123-45-67");
    submit(q);
    await settle();
    expect(calls("POST /contact").map((c) => c.body)).toEqual([{ phone: "+7 900 123-45-67", form: "offline" }]);
  });

  it("keeps the form and says so when the server did not take it", async () => {
    const { q } = mount({ offline: { fields: ["phone"] } });
    await settle();
    await type(q("#tk-pre-phone"), "+7 900 123-45-67");
    await type(q("#tk-pre-message"), "Позвоните");
    server.failNext.push({ route: "POST /contact", status: 500, code: "internal" });
    submit(q);
    await settle();
    expect(q("[role=alert]")?.textContent).toBe("Не удалось отправить. Повторите.");
    expect(calls("POST /messages")).toHaveLength(0);
    submit(q);
    await settle();
    expect(calls("POST /messages")).toHaveLength(1);
  });

  it("always asks for the email or the phone", () => {
    expect(offlineView({ fields: ["name"] }, undefined, "ru")?.fields).toEqual(["name", "email"]);
    expect(offlineView(undefined, { consent: { policyUrl: POLICY } }, "ru")).toEqual({
      fields: ["name", "email"],
      required: ["email"],
      intro: undefined,
      consent: { policyUrl: POLICY, text: undefined },
    });
    expect(offlineView({ fields: ["email", "phone"] }, undefined, "ru")?.required).toEqual([]);
    expect(offlineView(false, undefined, "ru")).toBeUndefined();
  });
});
