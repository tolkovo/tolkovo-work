import { afterEach, beforeEach, describe, expect, it } from "vitest";

import { FakeServer, FakeSocket } from "./test/fake";
import type { Options, TokenStorage } from "./types";
import { Widget, prechatView } from "./widget";

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

async function type(input: HTMLInputElement | null, value: string) {
  input!.value = value;
  input!.dispatchEvent(new Event("input", { bubbles: true }));
  await settle();
}

const submit = (q: ReturnType<typeof mount>["q"]) => q<HTMLButtonElement>("button.tk-start")!.click();
const contacts = () => server.calls.filter((c) => c.method === "POST" && c.path === "/contact");

const form = { fields: ["name", "email", "phone"], required: ["name"], consent: { policyUrl: POLICY } } satisfies Options["prechat"];

describe("pre-chat form", () => {
  beforeEach(() => {
    server = new FakeServer();
    server.install();
    FakeSocket.all = [];
  });
  afterEach(() => {
    for (const w of widgets) w.destroy();
    widgets = [];
  });

  it("asks who the visitor is before the first message, checks it and then opens the chat", async () => {
    const { w, q, storage } = mount({ prechat: form, open: false });
    const collected: unknown[] = [];
    w.controller.on("contactCollected", (p) => collected.push(p));
    await settle();
    q<HTMLButtonElement>("button.tk-launcher")!.click();
    await settle();
    expect(q("textarea")).toBeNull();
    expect(q(".tk-prechat-intro")?.textContent).toContain("Представьтесь");
    expect(q<HTMLAnchorElement>(".tk-consent a")?.href).toBe(POLICY);
    expect(w.shadowRoot.activeElement?.id).toBe("tk-pre-name");

    // Nothing filled in: the required name and the consent are explained.
    await type(q("#tk-pre-email"), "anna at mail");
    submit(q);
    await settle();
    expect(q("#tk-pre-name-error")?.textContent).toBe("Заполните поле.");
    expect(q("#tk-pre-name")?.getAttribute("aria-invalid")).toBe("true");
    expect(q("#tk-pre-email-error")?.textContent).toContain("name@example.com");
    expect(q("#tk-pre-consent-error")?.textContent).toBe("Без согласия начать чат нельзя.");
    expect(w.shadowRoot.activeElement?.id).toBe("tk-pre-name");
    expect(contacts()).toHaveLength(0);

    await type(q("#tk-pre-name"), "  Анна ");
    await type(q("#tk-pre-email"), "anna@example.com");
    q<HTMLInputElement>("#tk-pre-consent")!.click();
    await settle();
    submit(q);
    await settle();
    // Only what was filled in goes to the server; the phone was left empty.
    expect(contacts().map((c) => c.body)).toEqual([{ name: "Анна", email: "anna@example.com", consent: { policy_url: POLICY } }]);
    expect(q(".tk-prechat")).toBeNull();
    expect(q("textarea")).not.toBeNull();
    // No personal data in the event.
    expect(collected).toEqual([{ source: "prechat" }]);

    // The next page of the same visitor does not ask again.
    w.destroy();
    widgets = [];
    const again = mount({ prechat: form }, storage);
    await settle();
    await settle();
    expect(again.q(".tk-prechat")).toBeNull();
  });

  it("does not ask a visitor the site identified", async () => {
    const { q } = mount({ prechat: form, user: { id: "u-1", hash: "ab" } });
    await settle();
    await settle();
    expect(q(".tk-prechat")).toBeNull();
    expect(q("textarea")).not.toBeNull();
  });

  it("an optional form left empty closes without a request and stays closed", async () => {
    const { w, q, storage } = mount({ prechat: {} });
    await settle();
    expect([...w.shadowRoot.querySelectorAll(".tk-field label")].map((l) => l.textContent)).toEqual(["Имя", "Почта"]);
    submit(q);
    await settle();
    expect(contacts()).toHaveLength(0);
    expect(q("textarea")).not.toBeNull();
    w.destroy();
    widgets = [];
    const again = mount({ prechat: {} }, storage);
    await settle();
    expect(again.q(".tk-prechat")).toBeNull();
  });

  it("keeps the form and says so when the server did not take it", async () => {
    const { q } = mount({ prechat: { fields: ["phone"] } });
    await settle();
    await type(q("#tk-pre-phone"), "позвоните");
    submit(q);
    await settle();
    expect(q("#tk-pre-phone-error")).not.toBeNull();
    await type(q("#tk-pre-phone"), "+7 900 123-45-67");
    server.failNext.push({ route: "POST /contact", status: 500, code: "internal" });
    submit(q);
    await settle();
    expect(q("[role=alert]")?.textContent).toBe("Не удалось отправить. Повторите.");
    expect(q<HTMLButtonElement>("button.tk-start")?.disabled).toBe(false);
    submit(q);
    await settle();
    expect(contacts().at(-1)?.body).toEqual({ phone: "+7 900 123-45-67" });
    expect(q(".tk-prechat")).toBeNull();
  });

  it("ignores fields it does not know and a policy link that is not http(s)", () => {
    const view = prechatView({ fields: ["phone", "company" as never, "name"], required: ["email"], consent: { policyUrl: "javascript:alert(1)" } }, "ru");
    expect(view).toEqual({ fields: ["name", "phone"], required: [], intro: undefined, consent: undefined });
    expect(prechatView(undefined, "ru")).toBeUndefined();
  });
});
