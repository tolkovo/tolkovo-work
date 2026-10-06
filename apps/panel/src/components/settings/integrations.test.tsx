import type { GitHubIntegration, GitHubIntegrationState } from "@tolkovo/api-client";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import { fakeApi, json, operatorSession, problem } from "../../test/fake-api";
import { renderApp } from "../../test/render-app";

const defaults = {
  issue_title: "{summary}",
  issue_body: "## Что происходит\n{summary}\n",
  reply_ru: "Исправили, исправление войдёт в ближайшее обновление.",
  reply_en: "We have fixed it. The fix will be in the next update.",
};

const placeholders = {
  issue: ["summary", "steps", "expected", "actual", "app_name", "app_version", "os", "os_version", "conversation_url"],
  reply: ["name", "issue_number"],
};

const app = { id: 42, slug: "tolkovo-zerno", owner: "zerno-team", url: "https://github.com/apps/tolkovo-zerno" };

const active: GitHubIntegration = {
  id: "gh-1",
  status: "active",
  app,
  install_url: null,
  account: "zerno-team",
  repository: null,
  labels: ["bug"],
  templates: defaults,
  custom_templates: { issue_title: false, issue_body: false, reply_ru: false, reply_en: false },
  created_at: "2026-10-05T09:00:00Z",
  updated_at: "2026-10-05T09:00:00Z",
};

const state = (integration: GitHubIntegration | null, extra: Partial<GitHubIntegrationState> = {}) =>
  json(200, { integration, defaults, placeholders, encryption_available: true, ...extra });

afterEach(() => {
  vi.restoreAllMocks();
});

describe("integrations settings", () => {
  it("is for those who manage integrations", async () => {
    renderApp("/settings/profile", { api: fakeApi({ session: operatorSession }) });
    const nav = await screen.findByRole("navigation", { name: "Разделы настроек" });
    expect(within(nav).queryByRole("link", { name: "Интеграции" })).not.toBeInTheDocument();
  });

  it("says what goes to GitHub and sends the manifest there with a form", async () => {
    const user = userEvent.setup();
    const forms: HTMLFormElement[] = [];
    vi.spyOn(HTMLFormElement.prototype, "submit").mockImplementation(function (this: HTMLFormElement) {
      forms.push(this);
    });
    const api = fakeApi({
      routes: {
        "GET /integrations/github": () => state(null),
        "POST /integrations/github/manifest": () =>
          json(201, {
            integration: { ...active, status: "app_pending", app: null, account: null, labels: [] },
            post_url: "https://github.com/organizations/zerno-team/settings/apps/new?state=s1",
            manifest: '{"name":"Tolkovo 1a2b3c"}',
          }),
      },
    });
    renderApp("/settings/integrations", { api });
    expect(await screen.findByRole("note")).toHaveTextContent("Имя, почта и файлы клиента туда не попадают");
    await user.type(screen.getByLabelText("Организация на GitHub"), "zerno-team");
    await user.click(screen.getByRole("button", { name: "Создать GitHub App" }));

    await waitFor(() => expect(forms).toHaveLength(1));
    expect(api.calls.find((c) => c.method === "POST")?.body).toEqual({ organization: "zerno-team" });
    const form = forms[0]!;
    expect(form.method).toBe("post");
    expect(form.action).toBe("https://github.com/organizations/zerno-team/settings/apps/new?state=s1");
    expect((form.elements.namedItem("manifest") as HTMLInputElement).value).toBe('{"name":"Tolkovo 1a2b3c"}');
  });

  it("finishes the App when GitHub sends the browser back, then offers to install it", async () => {
    let current: GitHubIntegration | null = { ...active, status: "app_pending", app: null, account: null };
    const installing: GitHubIntegration = {
      ...active,
      status: "install_pending",
      account: null,
      install_url: "https://github.com/apps/tolkovo-zerno/installations/new",
    };
    const api = fakeApi({
      routes: {
        "GET /integrations/github": () => state(current),
        "POST /integrations/github/app": () => {
          current = installing;
          return json(200, installing);
        },
      },
    });
    const { router } = renderApp("/settings/integrations?github=created&code=c1&state=s1", { api });
    expect(await screen.findByRole("status")).toHaveTextContent("App создана. Осталось установить её на репозиторий.");
    expect(api.calls.find((c) => c.path === "/integrations/github/app")?.body).toEqual({ code: "c1", state: "s1" });
    expect(await screen.findByRole("link", { name: "Установить на GitHub" })).toHaveAttribute(
      "href",
      "https://github.com/apps/tolkovo-zerno/installations/new",
    );
    // The code leaves the address, so a reload does not send it again.
    await waitFor(() => expect(router.state.location.search).toEqual({}));
    expect(api.calls.filter((c) => c.path === "/integrations/github/app")).toHaveLength(1);
  });

  it("starts again from the first step while waiting for GitHub", async () => {
    const user = userEvent.setup();
    renderApp("/settings/integrations", {
      api: fakeApi({
        routes: { "GET /integrations/github": () => state({ ...active, status: "app_pending", app: null, account: null }) },
      }),
    });
    expect(await screen.findByText(/Подтвердите создание App на GitHub/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Начать заново" }));
    expect(screen.getByLabelText("Организация на GitHub")).toBeInTheDocument();
  });

  it("explains a refused return", async () => {
    const api = fakeApi({
      routes: {
        "GET /integrations/github": () => state({ ...active, status: "install_pending", account: null }),
        "POST /integrations/github/installation": () => problem(422, "github_installation_invalid"),
      },
    });
    renderApp("/settings/integrations?github=installed&installation_id=7&setup_action=install", { api });
    expect(await screen.findByText("Это установка чужой App. Установите App этого пространства.")).toBeInTheDocument();
    expect(api.calls.find((c) => c.path === "/integrations/github/installation")?.body).toEqual({ installation_id: 7 });
  });

  it("saves the repository, labels and templates, warning about a public repository", async () => {
    const user = userEvent.setup();
    const api = fakeApi({
      routes: {
        "GET /integrations/github": () => state(active),
        "GET /integrations/github/repositories": () =>
          json(200, {
            items: [
              { id: 1, full_name: "zerno-team/desktop", private: true, url: "https://github.com/zerno-team/desktop" },
              { id: 2, full_name: "zerno-team/site", private: false, url: "https://github.com/zerno-team/site" },
            ],
            more: false,
          }),
        "PATCH /integrations/github": () => json(200, active),
      },
    });
    renderApp("/settings/integrations", { api });
    expect(await screen.findByText("App «tolkovo-zerno», установлена в zerno-team.")).toBeInTheDocument();
    const repo = await screen.findByRole("combobox", { name: "Репозиторий" });
    await user.selectOptions(repo, "zerno-team/site");
    expect(screen.getByRole("note")).toHaveTextContent("Репозиторий публичный");
    await user.selectOptions(repo, "zerno-team/desktop");
    expect(screen.queryByText(/Репозиторий публичный/)).not.toBeInTheDocument();

    const labels = screen.getByLabelText("Метки");
    await user.clear(labels);
    await user.type(labels, "bug, from-support, ");
    const reply = screen.getByLabelText("Ответ клиенту об исправлении — по-русски");
    await user.clear(reply);
    await user.type(reply, "Исправили в новой версии.");
    expect(screen.getByRole("button", { name: "Вернуть стандартный" })).toBeInTheDocument();
    // Placeholders are listed with their meaning.
    expect(screen.getByText("{conversation_url}")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Сохранить" }));

    expect(await screen.findByText("Настройки GitHub сохранены.")).toBeInTheDocument();
    expect(api.calls.find((c) => c.method === "PATCH")?.body).toEqual({
      repository: "zerno-team/desktop",
      labels: ["bug", "from-support"],
      templates: { ...defaults, reply_ru: "Исправили в новой версии." },
    });
  });

  it("restores a default template", async () => {
    const user = userEvent.setup();
    const custom = { ...active, templates: { ...defaults, reply_en: "Fixed!" }, custom_templates: { ...active.custom_templates, reply_en: true } };
    const api = fakeApi({
      routes: {
        "GET /integrations/github": () => state(custom),
        "GET /integrations/github/repositories": () => problem(502, "github_unavailable"),
      },
    });
    renderApp("/settings/integrations", { api });
    const reply = await screen.findByLabelText("Ответ клиенту об исправлении — по-английски");
    expect(reply).toHaveValue("Fixed!");
    await user.click(screen.getByRole("button", { name: "Вернуть стандартный" }));
    expect(reply).toHaveValue(defaults.reply_en);
    // Without the list the repository is typed, and the reason is said.
    expect(await screen.findByLabelText("Репозиторий")).toHaveAccessibleDescription("GitHub не отвечает. Повторите через минуту.");
  });

  it("disconnects after a confirmation", async () => {
    const user = userEvent.setup();
    let current: GitHubIntegration | null = active;
    const api = fakeApi({
      routes: {
        "GET /integrations/github": () => state(current),
        "GET /integrations/github/repositories": () => json(200, { items: [], more: false }),
        "DELETE /integrations/github": () => {
          current = null;
          return new Response(null, { status: 204 });
        },
      },
    });
    renderApp("/settings/integrations", { api });
    await user.click(await screen.findByRole("button", { name: "Отключить GitHub" }));
    const group = screen.getByRole("group", { name: "Отключить GitHub" });
    expect(group).toHaveTextContent("Созданные issue останутся в GitHub");
    await user.click(within(group).getByRole("button", { name: "Отключить GitHub" }));
    expect(await screen.findByText(/GitHub отключён/)).toBeInTheDocument();
    expect(await screen.findByRole("button", { name: "Создать GitHub App" })).toBeInTheDocument();
    expect(api.calls.some((c) => c.method === "DELETE")).toBe(true);
  });

  it("cannot connect without an encryption key", async () => {
    renderApp("/settings/integrations", {
      api: fakeApi({ routes: { "GET /integrations/github": () => state(null, { encryption_available: false }) } }),
    });
    expect(await screen.findByRole("note")).toHaveTextContent("TOLKOVO_MASTER_KEY");
    expect(screen.queryByRole("button", { name: "Создать GitHub App" })).not.toBeInTheDocument();
  });
});
