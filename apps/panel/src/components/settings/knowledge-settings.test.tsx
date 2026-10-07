import type { KBArticle } from "@tolkovo/api-client";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { fakeApi, json, operatorSession, ownerSession } from "../../test/fake-api";
import { renderApp } from "../../test/render-app";

const admin = { ...ownerSession, permissions: [...ownerSession.permissions, "knowledge.manage"] };
const CRM_ID = "0199a000-0000-7000-8000-00000000c0a1";
const topics = [{ id: CRM_ID, name: "CRM", color: "blue", position: 0, priority: 0, task_queue_id: null, member_ids: [] }];
const emptyGuide = { about: "", handoff_rules: "", task_checklist: "", updated_at: null };

const entry = (id: string, patch: Partial<KBArticle> = {}): KBArticle => ({
  id,
  kind: "article",
  title: "Как подключить аккаунт",
  body: "Откройте «Настройки → Аккаунты».",
  url: null,
  published: true,
  topic_ids: [],
  created_at: "2026-10-07T10:00:00Z",
  updated_at: "2026-10-07T10:00:00Z",
  ...patch,
});

describe("knowledge base settings", () => {
  it("saves what the bot always knows and adds a question with its answer", async () => {
    const user = userEvent.setup();
    let articles: KBArticle[] = [];
    const api = fakeApi({
      session: admin,
      routes: {
        "GET /topics": () => json(200, { items: topics }),
        "GET /kb/guide": () => json(200, emptyGuide),
        "PUT /kb/guide": async (r) => json(200, { ...((await r.json()) as object), updated_at: "2026-10-07T10:00:00Z" }),
        "GET /kb/articles": () => json(200, { items: articles }),
        "POST /kb/articles": async (r) => {
          const body = (await r.json()) as KBArticle;
          articles = [entry("a1", { ...body })];
          return json(201, articles[0]);
        },
      },
    });
    renderApp("/settings/knowledge", { api });
    expect(await screen.findByRole("heading", { level: 2, name: "База знаний" })).toBeInTheDocument();

    await user.type(await screen.findByLabelText("О компании и продукте"), "Зерно — CRM для агентств.");
    await user.type(screen.getByLabelText("Когда звать оператора"), "Оплата и возвраты.");
    await user.type(screen.getByLabelText("Что собрать для задачи в ИТ"), "Email аккаунта");
    await user.click(screen.getByRole("button", { name: "Сохранить" }));
    await waitFor(() =>
      expect(api.calls.find((c) => c.method === "PUT")?.body).toEqual({ about: "Зерно — CRM для агентств.", handoff_rules: "Оплата и возвраты.", task_checklist: "Email аккаунта" }),
    );
    expect(await screen.findByText("Сохранено.")).toBeInTheDocument();

    expect(screen.getByText(/Записей пока нет/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Добавить вопрос-ответ" }));
    await user.click(screen.getByRole("button", { name: "Сохранить запись" }));
    expect(screen.getByLabelText("Вопрос")).toHaveAccessibleDescription(/Напишите вопрос/);
    await user.type(screen.getByLabelText("Вопрос"), "Есть ли экспорт в Excel?");
    await user.type(screen.getByLabelText("Ответ"), "Пока нет.");
    await user.click(screen.getByRole("checkbox", { name: "CRM" }));
    await user.click(screen.getByRole("button", { name: "Сохранить запись" }));
    await waitFor(() =>
      expect(api.calls.find((c) => c.method === "POST")?.body).toEqual({
        kind: "qa",
        title: "Есть ли экспорт в Excel?",
        body: "Пока нет.",
        url: null,
        published: true,
        topic_ids: [CRM_ID],
      }),
    );
    const item = (await screen.findByText("Есть ли экспорт в Excel?")).closest("li")!;
    expect(within(item).getByText("CRM")).toBeInTheDocument();
  });

  it("imports a knowledge file and names the topics it could not find", async () => {
    const user = userEvent.setup();
    const api = fakeApi({
      session: admin,
      routes: {
        "GET /topics": () => json(200, { items: topics }),
        "GET /kb/guide": () => json(200, emptyGuide),
        "GET /kb/articles": () => json(200, { items: [] }),
        "POST /kb/import": () => json(200, { created: 2, updated: 1, unknown_topics: ["Fansly"] }),
      },
    });
    renderApp("/settings/knowledge", { api });
    const input = await screen.findByLabelText("Выбрать файл…");

    await user.upload(input, new File(["not json"], "kb.json", { type: "application/json" }));
    expect(await screen.findByText("Это не JSON-файл.")).toBeInTheDocument();
    await user.upload(input, new File([JSON.stringify({ format: "other", articles: [] })], "kb.json", { type: "application/json" }));

    expect(await screen.findByText(/нужен формат «tolkovo-knowledge\/1»/)).toBeInTheDocument();
    expect(api.calls.some((c) => c.method === "POST")).toBe(false);

    const file = { format: "tolkovo-knowledge/1", about: "О нас", articles: [{ kind: "qa", title: "Цена?", body: "10 $", topics: ["Fansly"] }] };
    await user.upload(input, new File([JSON.stringify(file)], "kb.json", { type: "application/json" }));
    expect(await screen.findByText("Добавлено записей: 2, обновлено: 1. Таких тем нет, привязка пропущена: Fansly.")).toBeInTheDocument();
    expect(api.calls.find((c) => c.method === "POST")?.body).toEqual(file);
  });

  it("shows what the bot would find", async () => {
    const user = userEvent.setup();
    const asked: (string | null)[] = [];
    const api = fakeApi({
      session: admin,
      routes: {
        "GET /topics": () => json(200, { items: topics }),
        "GET /kb/guide": () => json(200, emptyGuide),
        "GET /kb/articles": () => json(200, { items: [entry("a1")] }),
        "GET /kb/search": (r) => {
          const q = new URL(r.url).searchParams.get("q");
          asked.push(new URL(r.url).searchParams.get("topic"));
          return json(200, {
            items: q === "подключить аккаунт" ? [{ id: "a1", kind: "article", title: "Как подключить аккаунт", url: null, excerpt: "Откройте настройки." }] : [],
          });
        },
      },
    });
    renderApp("/settings/knowledge", { api });
    const search = await screen.findByRole("search");
    await user.type(within(search).getByLabelText("Вопрос клиента"), "подключить аккаунт");
    await user.selectOptions(within(search).getByLabelText("Тема диалога"), CRM_ID);
    await user.click(within(search).getByRole("button", { name: "Найти" }));
    expect(await screen.findByText("Бот получит 1 запись:")).toBeInTheDocument();
    expect(asked).toEqual([CRM_ID]);

    await user.clear(within(search).getByLabelText("Вопрос клиента"));
    await user.type(within(search).getByLabelText("Вопрос клиента"), "возврат денег");
    await user.click(within(search).getByRole("button", { name: "Найти" }));
    expect(await screen.findByText(/Ничего не нашлось/)).toBeInTheDocument();
  });

  it("is not in the tree without knowledge.manage", async () => {
    renderApp("/settings/team", { api: fakeApi({ session: operatorSession }) });
    await screen.findByRole("heading", { level: 2, name: "Команда" });
    expect(screen.queryByRole("link", { name: "База знаний" })).not.toBeInTheDocument();
  });
});
