import type { Session, Task, TaskActivity, TaskFile } from "@tolkovo/api-client";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import { fakeApi, json, ownerSession, problem, type FakeRoutes } from "../../test/fake-api";
import { renderApp } from "../../test/render-app";

/** Files of tasks: chosen, pasted or dropped, uploaded by signed link, sent with the task or comment. */

const member: Session = { ...ownerSession, permissions: [...ownerSession.permissions, "tasks.edit", "tasks.view"] };

const shot: TaskFile = {
  id: "f-shot",
  file_name: "экран.png",
  content_type: "image/png",
  size: 2048,
  uploaded_by: member.user.id,
  created_at: "2026-10-07T10:00:00Z",
};
const log: TaskFile = { ...shot, id: "f-log", file_name: "app.log", content_type: "text/plain", size: 300 };

const baseTask: Task = {
  id: "t1",
  number: 12,
  queue_id: null,
  topic_id: null,
  title: "Кнопка не нажимается",
  body: "",
  status: "open",
  priority: "normal",
  assignee_id: null,
  created_by: member.user.id,
  created_at: "2026-10-07T10:00:00Z",
  updated_at: "2026-10-07T10:00:00Z",
  closed_at: null,
  status_changed_at: "2026-10-07T10:00:00Z",
  version: 1,
  conversation_ids: [],
  conversation_count: 0,
  tag_ids: [],
  files: [shot],
};

function server(routes: FakeRoutes = {}) {
  const state = { task: baseTask, feed: [] as TaskActivity[], uploads: 0 };
  const api = fakeApi({
    session: member,
    routes: {
      "GET /tasks": () => json(200, { items: [state.task], next_cursor: null }),
      "GET /tasks/summary": () => json(200, { assigned_open: 0 }),
      "GET /tasks/t1": () => json(200, state.task),
      "GET /tasks/t1/activity": () => json(200, { items: state.feed }),
      "GET /task-queues": () => json(200, { items: [] }),
      "GET /task-settings": () =>
        json(200, {
          settings: { enabled: true, conversation_button: "both", reply_ru: "Готово", reply_en: "Done" },
          defaults: { reply_ru: "Готово", reply_en: "Done" },
          placeholders: ["name", "task_number"],
        }),
      "GET /task-files/f-new-1/download-link": () =>
        json(200, { method: "GET", url: "http://panel.test/files/new", headers: {}, expires_at: "2026-10-07T10:15:00Z" }),
      "GET /task-files/f-shot/download-link": () =>
        json(200, { method: "GET", url: "http://panel.test/files/shot", headers: {}, expires_at: "2026-10-07T10:15:00Z" }),
      "POST /task-files": async (request) => {
        const body = (await request.json()) as { file_name: string; content_type: string; size: number };
        const id = `f-new-${++state.uploads}`;
        return json(201, {
          file: { ...log, id, file_name: body.file_name, content_type: body.content_type, size: body.size },
          upload: { method: "PUT", url: `http://panel.test/files/${id}`, headers: { "Content-Type": body.content_type }, expires_at: "2026-10-07T11:00:00Z" },
        });
      },
      "PUT /files/f-new-1": () => new Response(null, { status: 204 }),
      "PUT /files/f-new-2": () => new Response(null, { status: 204 }),
      "POST /tasks/t1/activity": async (request) => {
        const body = (await request.json()) as { body: string; file_ids?: string[] };
        const comment: TaskActivity = {
          id: "c1",
          actor_id: member.user.id,
          kind: "comment",
          body: body.body,
          files: (body.file_ids ?? []).map((id) => ({ ...shot, id, file_name: "screenshot.png" })),
          created_at: "2026-10-07T10:05:00Z",
        };
        state.feed = [comment];
        return json(201, comment);
      },
      "POST /tasks/t1/files": async (request) => {
        const body = (await request.json()) as { file_ids: string[] };
        state.task = { ...state.task, files: [...state.task.files, ...body.file_ids.map((id) => ({ ...log, id }))] };
        return json(200, state.task);
      },
      "DELETE /tasks/t1/files/f-shot": () => {
        state.task = { ...state.task, files: state.task.files.filter((f) => f.id !== "f-shot") };
        return new Response(null, { status: 204 });
      },
      ...routes,
    },
  });
  return { api, state };
}

const calls = (api: ReturnType<typeof fakeApi>, method: string, path: string) => api.calls.filter((c) => c.method === method && c.path === path);

describe("files of tasks", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("shows the description's screenshot and takes a file out", async () => {
    const user = userEvent.setup();
    const { api } = server();
    vi.stubGlobal("fetch", api.fetch);
    renderApp("/tasks/t1", { api });
    const files = await screen.findByRole("region", { name: "Файлы" }, { timeout: 3000 });
    expect(await within(files).findByRole("button", { name: /^Открыть.*экран\.png/ })).toBeInTheDocument();
    await user.click(within(files).getByRole("button", { name: "Убрать «экран.png» из задачи" }));
    await waitFor(() => expect(calls(api, "DELETE", "/tasks/t1/files/f-shot")).toHaveLength(1));
  });

  it("sends a pasted screenshot with a comment", async () => {
    const user = userEvent.setup();
    const { api } = server();
    vi.stubGlobal("fetch", api.fetch);
    renderApp("/tasks/t1", { api });
    const box = await screen.findByPlaceholderText(/комментар/i, {}, { timeout: 3000 });
    const png = new File([new Uint8Array([137, 80, 78, 71])], "image.png", { type: "image/png" });
    fireEvent.paste(box, { clipboardData: { items: [{ kind: "file", getAsFile: () => png }] } });

    const chosen = await screen.findByRole("list", { name: "Выбранные файлы" });
    expect(within(chosen).getByText(/^screenshot-.*\.png$/)).toBeInTheDocument();
    await waitFor(() => expect(calls(api, "PUT", "/files/f-new-1")).toHaveLength(1));
    const register = calls(api, "POST", "/task-files")[0];
    expect(register?.body).toMatchObject({ content_type: "image/png", size: 4 });

    // A comment of the screenshot only.
    await user.click(screen.getByRole("button", { name: "Отправить" }));
    await waitFor(() => expect(calls(api, "POST", "/tasks/t1/activity")).toHaveLength(1));
    expect(calls(api, "POST", "/tasks/t1/activity")[0]?.body).toEqual({ body: "", file_ids: ["f-new-1"] });
    await waitFor(() => expect(screen.queryByRole("list", { name: "Выбранные файлы" })).not.toBeInTheDocument());
  });

  it("adds a chosen log to the description at once and refuses what the server would not take", async () => {
    const user = userEvent.setup({ applyAccept: false });
    const { api } = server();
    vi.stubGlobal("fetch", api.fetch);
    renderApp("/tasks/t1", { api });
    const files = await screen.findByRole("region", { name: "Файлы" }, { timeout: 3000 });
    const input = files.querySelector<HTMLInputElement>('input[type="file"]');
    if (!input) throw new Error("no file input");
    const exe = new File(["MZ"], "setup.exe", { type: "application/x-msdownload" });
    const logFile = new File(["12:00 ERROR\n"], "app.log", { type: "" });
    await user.upload(input, [exe, logFile]);

    expect(await within(files).findByText("такой тип файла нельзя прикрепить")).toBeInTheDocument();
    await waitFor(() => expect(calls(api, "POST", "/tasks/t1/files")).toHaveLength(1));
    expect(calls(api, "POST", "/task-files")[0]?.body).toMatchObject({ file_name: "app.log", content_type: "text/plain" });
    expect(calls(api, "POST", "/tasks/t1/files")[0]?.body).toEqual({ file_ids: ["f-new-1"] });
  });

  it("explains a file the server refused", async () => {
    const user = userEvent.setup();
    const { api } = server({ "POST /tasks/t1/files": () => problem(422, "file_rejected") });
    vi.stubGlobal("fetch", api.fetch);
    renderApp("/tasks/t1", { api });
    const files = await screen.findByRole("region", { name: "Файлы" }, { timeout: 3000 });
    const input = files.querySelector<HTMLInputElement>('input[type="file"]');
    if (!input) throw new Error("no file input");
    await user.upload(input, new File(["not a png"], "fake.png", { type: "image/png" }));
    expect(await within(files).findByText(/не прошёл проверку/)).toBeInTheDocument();
  });
});
