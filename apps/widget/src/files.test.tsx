import { afterEach, beforeEach, describe, expect, it } from "vitest";

import { fileType, formatSize } from "./files";
import { FakeServer, FakeSocket, fakeFile, nextId } from "./test/fake";
import type { TokenStorage } from "./types";
import { Widget } from "./widget";

const KEY = "tk_widget_test";

function memory(): TokenStorage {
  const data = new Map<string, string>();
  return { getItem: (k) => data.get(k) ?? null, setItem: (k, v) => void data.set(k, v), removeItem: (k) => void data.delete(k) };
}

async function settle(): Promise<void> {
  for (let i = 0; i < 10; i++) await new Promise((r) => setTimeout(r, 0));
}

const png = (name = "screen.png") => new File([new Uint8Array([0x89, 0x50, 0x4e, 0x47, 1, 2, 3])], name, { type: "image/png" });
const log = () => new File(["12:00 ERROR корзина пуста\n"], "app.log", { type: "" });

let server: FakeServer;
let widgets: Widget[] = [];

function mount() {
  const w = new Widget({ widgetKey: KEY, apiBase: "https://support.example.com", locale: "ru", storage: memory(), open: true }, FakeSocket.factory);
  widgets.push(w);
  const q = <T extends Element>(sel: string) => w.shadowRoot.querySelector<T>(sel);
  const all = (sel: string) => [...w.shadowRoot.querySelectorAll(sel)];
  return { w, q, all };
}

/** Picks files as the visitor would in the file dialog. */
function pick(input: HTMLInputElement, files: File[]) {
  Object.defineProperty(input, "files", { value: files, configurable: true });
  input.dispatchEvent(new Event("change", { bubbles: true }));
}

const route = (r: string) => server.calls.filter((c) => `${c.method} ${c.path.split("?")[0]}` === r);
const puts = () => server.calls.filter((c) => c.method === "PUT");

describe("files in the widget", () => {
  beforeEach(() => {
    server = new FakeServer();
    server.install();
    FakeSocket.all = [];
  });
  afterEach(() => {
    for (const w of widgets) w.destroy();
    widgets = [];
  });

  it("attaches a screenshot and a log, uploads them and shows them in the chat", async () => {
    const { q, all } = mount();
    await settle();
    expect(q("button.tk-attach")?.getAttribute("aria-label")).toBe("Прикрепить файл");
    pick(q<HTMLInputElement>("input[type=file]")!, [png(), log()]);
    await settle();
    expect(all(".tk-chip .tk-file-name").map((n) => n.textContent)).toEqual(["screen.png", "app.log"]);
    expect(q(".tk-chip img")?.getAttribute("src")).toBe("blob:fake/screen.png");
    // Files alone are enough to send.
    expect(q<HTMLButtonElement>("button.tk-send")!.disabled).toBe(false);

    q<HTMLTextAreaElement>("textarea")!.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true }));
    await settle();
    expect(route("POST /uploads").map((c) => c.body)).toEqual([
      { file_name: "screen.png", content_type: "image/png", size: 7 },
      // The browser left the log's type empty: the extension tells.
      { file_name: "app.log", content_type: "text/plain", size: new Blob(["12:00 ERROR корзина пуста\n"]).size },
    ]);
    expect(puts()).toHaveLength(2);
    expect(puts()[0]!.headers["Content-Type"]).toBe("image/png");
    const sent = route("POST /messages")[0]!.body as { message?: string; attachment_ids: string[] };
    expect(sent.message).toBeUndefined();
    expect(sent.attachment_ids).toHaveLength(2);
    expect(all(".tk-chip")).toHaveLength(0);

    const mine = q(".tk-msg[data-from='contact']:not([data-pending])")!;
    expect(mine.querySelector(".tk-bubble")).toBeNull();
    const thumb = mine.querySelector<HTMLAnchorElement>("a.tk-thumb")!;
    expect(thumb.target).toBe("_blank");
    expect(thumb.rel).toBe("noopener noreferrer");
    expect(thumb.querySelector("img")?.getAttribute("alt")).toBe("Картинка screen.png");
    expect(mine.querySelector("a.tk-file .tk-file-name")?.textContent).toBe("app.log");
  });

  it("explains files it will not send, and takes pasted screenshots", async () => {
    const { q, all } = mount();
    await settle();
    const input = q<HTMLInputElement>("input[type=file]")!;
    pick(input, [new File(["MZ"], "setup.exe", { type: "application/x-msdownload" })]);
    await settle();
    expect(q(".tk-file-error")?.textContent).toBe("«setup.exe»: такие файлы не принимаются. Подойдут картинки, PDF, текст, таблицы и архивы.");
    const big = new File(["x"], "video.png", { type: "image/png" });
    Object.defineProperty(big, "size", { value: 10 * 1024 * 1024 + 1 });
    pick(input, [big]);
    await settle();
    expect(q(".tk-file-error")?.textContent).toBe("«video.png» больше 10 МБ.");
    pick(input, [png("1.png"), png("2.png"), png("3.png"), png("4.png"), png("5.png"), png("6.png")]);
    await settle();
    expect(all(".tk-chip")).toHaveLength(5);
    expect(q(".tk-file-error")?.textContent).toBe("Не больше 5 файлов в сообщении.");
    // Removing a file clears the note.
    q<HTMLButtonElement>(".tk-chip-remove[aria-label='Убрать 5.png']")!.click();
    await settle();
    expect(all(".tk-chip")).toHaveLength(4);
    expect(q(".tk-file-error")).toBeNull();

    const paste = new Event("paste", { bubbles: true, cancelable: true });
    Object.defineProperty(paste, "clipboardData", { value: { files: [png("clipboard.png")] } });
    q("textarea")!.dispatchEvent(paste);
    await settle();
    expect(paste.defaultPrevented).toBe(true);
    expect(all(".tk-chip .tk-file-name").at(-1)?.textContent).toBe("clipboard.png");
  });

  it("sends the rest when the server refuses a file, without uploading again", async () => {
    const { w, q } = mount();
    await settle();
    server.reject.add("fake.png");
    await w.controller.send("Смотрите", [png("fake.png"), png("real.png")]);
    await settle();
    expect(q(".tk-failed")?.textContent).toContain("Файл «fake.png» не прошёл проверку и не отправлен.");
    expect(puts()).toHaveLength(2);

    q<HTMLButtonElement>(".tk-failed button")!.click();
    await settle();
    expect(q(".tk-failed")).toBeNull();
    // real.png was uploaded already; only the message goes again.
    expect(puts()).toHaveLength(2);
    const last = route("POST /messages").at(-1)!.body as { message: string; attachment_ids: string[] };
    expect(last.message).toBe("Смотрите");
    expect(last.attachment_ids).toHaveLength(1);
    expect(q(".tk-msg[data-from='contact'] .tk-file-name, .tk-msg[data-from='contact'] img")?.getAttribute("alt")).toBe("Картинка real.png");
  });

  it("shows an operator's file and renews links that ran out", async () => {
    const { w, q } = mount();
    await settle();
    w.controller.setOpen(false);
    server.messages.push({
      id: nextId(),
      conversation_id: "conv-1",
      sender: "operator",
      body: "Инструкция во вложении",
      created_at: "2026-10-05T10:00:00Z",
      files: [fakeFile("guide.pdf", "application/pdf", 1_300_000, "2000-01-01T00:00:00Z")],
    });
    FakeSocket.last().receive("widget.message");
    await settle();
    server.linkVersion = 2;
    w.controller.setOpen(true);
    await settle();
    const file = q<HTMLAnchorElement>(".tk-msg[data-from='operator'] a.tk-file")!;
    expect(file.querySelector(".tk-file-name")?.textContent).toBe("guide.pdf");
    expect(file.querySelector(".tk-file-size")?.textContent).toBe("1,2 МБ");
    expect(file.href).toContain("v=2");
    expect(route("GET /messages").some((c) => !c.path.includes("since"))).toBe(true);
  });
});

describe("file checks", () => {
  it("declares the type by extension when the browser does not", () => {
    expect(fileType(new File([""], "a.log", { type: "" }))).toBe("text/plain");
    expect(fileType(new File([""], "a.json", { type: "application/octet-stream" }))).toBe("application/json");
    expect(fileType(new File([""], "notes.md", { type: "text/markdown" }))).toBeNull();
    expect(fileType(new File([""], "a.png", { type: "image/svg+xml" }))).toBeNull();
    expect(fileType(new File([""], "Doc.DOCX", { type: "application/vnd.openxmlformats-officedocument.wordprocessingml.document" }))).toBe(
      "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
    );
    expect(formatSize(340 * 1024, "ru")).toBe("340 КБ");
    expect(formatSize(1_300_000, "en")).toBe("1.2 MB");
    expect(formatSize(93, "ru")).toBe("93 Б");
    expect(formatSize(10, "en")).toBe("10 B");
  });
});
