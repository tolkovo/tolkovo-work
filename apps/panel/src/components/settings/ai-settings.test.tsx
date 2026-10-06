import type { AISettings } from "@tolkovo/api-client";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { aiSettingsOff, fakeApi, json, operatorSession, ownerSession, problem } from "../../test/fake-api";
import { renderApp } from "../../test/render-app";

const admin = { ...ownerSession, permissions: [...ownerSession.permissions, "ai.manage"] };

const haiku: AISettings = {
  ...aiSettingsOff,
  enabled: true,
  provider: "anthropic",
  model: "claude-haiku-4-5",
  key: { hint: "…cdef", set_at: "2026-10-06T10:00:00Z" },
  prices: { input_micros: 1_000_000, output_micros: 5_000_000 },
  monthly_budget_micros: 10_000_000,
};

describe("AI settings", () => {
  it("sets up Anthropic with a key, a suggested model, its prices and a budget", async () => {
    const user = userEvent.setup();
    const api = fakeApi({
      session: admin,
      routes: { "PUT /ai/settings": () => json(200, haiku) },
    });
    renderApp("/settings/ai", { api });
    await screen.findByRole("heading", { level: 2, name: "Провайдер, ключ и расход" });
    expect(await screen.findByText(/^Потрачено с 1 октября: 0,00\s\$, бюджет не задан$/)).toBeInTheDocument();

    await user.click(screen.getByRole("radio", { name: "Anthropic" }));
    await user.type(screen.getByLabelText("Ключ API"), "sk-ant-api03-0123456789abcdef");
    await user.type(screen.getByLabelText("Модель"), "claude-haiku-4-5");
    // A suggested model brings its list prices.
    expect(screen.getByLabelText("Входящие токены")).toHaveValue("1");
    expect(screen.getByLabelText("Ответ модели")).toHaveValue("5");
    await user.type(screen.getByLabelText("Бюджет в месяц, $"), "10");
    await user.click(screen.getByRole("checkbox", { name: "Включить ИИ" }));
    await user.click(screen.getByRole("button", { name: "Сохранить" }));

    expect(await screen.findByText("Сохранено.")).toBeInTheDocument();
    expect(api.calls.find((c) => c.method === "PUT")?.body).toEqual({
      enabled: true,
      provider: "anthropic",
      model: "claude-haiku-4-5",
      base_url: null,
      api_key: "sk-ant-api03-0123456789abcdef",
      prices: { input_micros: 1_000_000, output_micros: 5_000_000 },
      monthly_budget_micros: 10_000_000,
    });
    // The key field is emptied and the saved key is named by its hint only.
    expect(screen.getByLabelText("Ключ API")).toHaveValue("");
    expect(screen.getByLabelText("Ключ API")).toHaveAccessibleDescription("Сохранён ключ …cdef. Введите новый, чтобы заменить.");
  });

  it("keeps the saved key when the field is left empty and warns before another provider drops it", async () => {
    const user = userEvent.setup();
    const api = fakeApi({
      session: admin,
      routes: { "GET /ai/settings": () => json(200, haiku), "PUT /ai/settings": async (r) => json(200, { ...haiku, ...(await r.json()) }) },
    });
    renderApp("/settings/ai", { api });
    const budget = await screen.findByLabelText("Бюджет в месяц, $");
    await user.clear(budget);
    await user.type(budget, "25,5");
    await user.click(screen.getByRole("button", { name: "Сохранить" }));
    await screen.findByText("Сохранено.");
    const body = api.calls.find((c) => c.method === "PUT")?.body as Record<string, unknown>;
    expect(body).not.toHaveProperty("api_key");
    expect(body.monthly_budget_micros).toBe(25_500_000);

    await user.click(screen.getByRole("radio", { name: "OpenAI" }));
    expect(screen.getByLabelText("Ключ API")).toHaveAccessibleDescription(/относится к другому провайдеру и будет удалён/);
  });

  it("says what is missing before turning AI on", async () => {
    const user = userEvent.setup();
    const api = fakeApi({ session: admin });
    renderApp("/settings/ai", { api });
    await user.click(await screen.findByRole("checkbox", { name: "Включить ИИ" }));
    await user.click(screen.getByRole("button", { name: "Сохранить" }));
    expect(screen.getByText("Выберите провайдера.")).toBeInTheDocument();
    expect(screen.getByRole("radio", { name: "Anthropic" })).toHaveFocus();

    await user.click(screen.getByRole("radio", { name: "Anthropic" }));
    await user.click(screen.getByRole("button", { name: "Сохранить" }));
    expect(screen.getByLabelText("Модель")).toHaveAccessibleDescription(/Чтобы включить ИИ, укажите модель/);
    expect(screen.getByLabelText("Модель")).toHaveFocus();
    await user.type(screen.getByLabelText("Модель"), "claude-haiku-4-5");
    await user.click(screen.getByRole("button", { name: "Сохранить" }));
    expect(screen.getByLabelText("Ключ API")).toHaveAccessibleDescription(/Чтобы включить ИИ, введите ключ API/);
    expect(api.calls.some((c) => c.method === "PUT")).toBe(false);
  });

  it("sets up DeepSeek with a key and its list prices, and says where its servers are", async () => {
    const user = userEvent.setup();
    const api = fakeApi({ session: admin, routes: { "PUT /ai/settings": async (r) => json(200, { ...aiSettingsOff, ...(await r.json()) }) } });
    renderApp("/settings/ai", { api });
    const radio = await screen.findByRole("radio", { name: "DeepSeek" });
    expect(radio).toHaveAccessibleDescription(/Серверы DeepSeek в Китае/);
    await user.click(radio);
    expect(screen.queryByLabelText("Адрес сервера")).not.toBeInTheDocument();
    await user.type(screen.getByLabelText("Ключ API"), "sk-0123456789abcdef");
    await user.type(screen.getByLabelText("Модель"), "deepseek-chat");
    expect(screen.getByLabelText("Входящие токены")).toHaveValue("0.28");
    expect(screen.getByLabelText("Ответ модели")).toHaveValue("0.42");
    await user.click(screen.getByRole("button", { name: "Сохранить" }));
    await screen.findByText("Сохранено.");
    expect(api.calls.find((c) => c.method === "PUT")?.body).toMatchObject({
      provider: "deepseek",
      model: "deepseek-chat",
      base_url: null,
      api_key: "sk-0123456789abcdef",
      prices: { input_micros: 280_000, output_micros: 420_000 },
    });
  });

  it("asks an OpenAI-compatible server for its address, not a key", async () => {
    const user = userEvent.setup();
    const api = fakeApi({ session: admin, routes: { "PUT /ai/settings": async (r) => json(200, { ...aiSettingsOff, ...(await r.json()) }) } });
    renderApp("/settings/ai", { api });
    await user.click(await screen.findByRole("radio", { name: "Совместимый с OpenAI" }));
    expect(screen.getByLabelText("Ключ API, если сервер его требует")).toBeInTheDocument();
    await user.type(screen.getByLabelText("Модель"), "qwen2.5:7b");
    await user.click(screen.getByRole("checkbox", { name: "Включить ИИ" }));
    await user.click(screen.getByRole("button", { name: "Сохранить" }));
    expect(screen.getByLabelText("Адрес сервера")).toHaveFocus();
    await user.type(screen.getByLabelText("Адрес сервера"), "http://ollama:11434/v1");
    await user.click(screen.getByRole("button", { name: "Сохранить" }));
    await screen.findByText("Сохранено.");
    expect(api.calls.find((c) => c.method === "PUT")?.body).toMatchObject({
      enabled: true,
      provider: "openai_compatible",
      base_url: "http://ollama:11434/v1",
      prices: { input_micros: 0, output_micros: 0 },
    });
  });

  it("shows the month's spending against the budget", async () => {
    renderApp("/settings/ai", {
      api: fakeApi({ session: admin, routes: { "GET /ai/settings": () => json(200, { ...haiku, spent_micros: 8_500_000 }) } }),
    });
    expect(await screen.findByText(/^Потрачено с 1 октября: 8,50\s\$ из 10,00\s\$$/)).toBeInTheDocument();
    expect(screen.getByRole("meter", { name: "Израсходовано бюджета" })).toHaveAttribute("aria-valuenow", "85");
    expect(screen.getByText("Израсходовано больше 80% бюджета.")).toBeInTheDocument();
  });

  it("checks the connection and explains a refused key", async () => {
    const user = userEvent.setup();
    let answer = json(200, { model: "claude-haiku-4-5-20251001", latency_ms: 640 });
    const api = fakeApi({
      session: admin,
      routes: { "GET /ai/settings": () => json(200, haiku), "POST /ai/settings/check": () => answer },
    });
    renderApp("/settings/ai", { api });
    await user.click(await screen.findByRole("button", { name: "Проверить подключение" }));
    expect(await screen.findByText("Модель claude-haiku-4-5-20251001 ответила за 640 мс.")).toBeInTheDocument();

    answer = problem(422, "ai_key_refused");
    await user.click(screen.getByRole("button", { name: "Проверить подключение" }));
    expect(await screen.findByText(/Провайдер не принял ключ API/)).toBeInTheDocument();
  });

  it("warns when the server cannot store a key", async () => {
    renderApp("/settings/ai", {
      api: fakeApi({ session: admin, routes: { "GET /ai/settings": () => json(200, { ...haiku, enabled: false, key: null, encryption_available: false }) } }),
    });
    expect(await screen.findByText(/На сервере не задан TOLKOVO_MASTER_KEY/)).toHaveAttribute("role", "alert");
    expect(screen.getByLabelText("Ключ API")).toBeDisabled();
  });

  it("is not in the tree without ai.manage", async () => {
    renderApp("/settings/team", { api: fakeApi({ session: operatorSession }) });
    await screen.findByRole("heading", { level: 2, name: "Команда" });
    expect(screen.queryByRole("link", { name: "ИИ" })).not.toBeInTheDocument();
  });
});
