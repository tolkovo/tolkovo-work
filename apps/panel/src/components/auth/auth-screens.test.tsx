import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { fakeApi, json, ownerSession, problem } from "../../test/fake-api";
import { renderApp } from "../../test/render-app";

const signedOut = () => fakeApi({ session: null });

describe("auth guard", () => {
  it("sends a signed-out visitor to sign-in and keeps the return path", async () => {
    const { router } = renderApp("/settings/team?tab=1", { api: signedOut() });
    expect(await screen.findByRole("heading", { level: 1, name: "Вход" })).toBeInTheDocument();
    expect(router.state.location.pathname).toBe("/login");
    expect(router.state.location.search).toEqual({ redirect: "/settings/team?tab=1" });
  });

  it("brings the person back to the return path after sign-in", async () => {
    const user = userEvent.setup();
    const api = fakeApi({ session: null, routes: { "POST /auth/login": () => json(200, ownerSession) } });
    const { router } = renderApp("/settings/team", { api });
    await screen.findByRole("heading", { level: 1, name: "Вход" });

    await user.type(screen.getByLabelText("Эл. почта"), "anna@zerno.test");
    await user.type(screen.getByLabelText("Пароль"), "correct horse");
    await user.click(screen.getByRole("button", { name: "Войти" }));

    expect(await screen.findByRole("heading", { level: 2, name: "Команда" })).toBeInTheDocument();
    expect(router.state.location.pathname).toBe("/settings/team");
    expect(api.calls.find((c) => c.path === "/auth/login")?.body).toEqual({
      email: "anna@zerno.test",
      password: "correct horse",
    });
  });

  it.each(["//evil.example/x", "https://evil.example", "/\\evil.example", "javascript:alert(1)"])(
    "ignores an unsafe return path %j and signs in to the inbox",
    async (redirect) => {
      const user = userEvent.setup();
      const api = fakeApi({ session: null, routes: { "POST /auth/login": () => json(200, ownerSession) } });
      const { router } = renderApp(`/login?redirect=${encodeURIComponent(redirect)}`, { api });
      await screen.findByRole("heading", { level: 1, name: "Вход" });
      await user.type(screen.getByLabelText("Эл. почта"), "anna@zerno.test");
      await user.type(screen.getByLabelText("Пароль"), "correct horse");
      await user.click(screen.getByRole("button", { name: "Войти" }));
      expect(await screen.findByRole("heading", { level: 1, name: "Диалоги" })).toBeInTheDocument();
      expect(router.state.location.href).toBe("/inbox");
    },
  );

  it("sends a signed-in person from sign-in to the inbox", async () => {
    const { router } = renderApp("/login");
    expect(await screen.findByRole("heading", { level: 1, name: "Диалоги" })).toBeInTheDocument();
    expect(router.state.location.pathname).toBe("/inbox");
  });

  it("signs out to the sign-in screen when the session ends mid-work", async () => {
    let alive = true;
    const api = fakeApi({
      routes: {
        "GET /me": () => (alive ? json(200, ownerSession) : problem(401, "unauthenticated")),
        "GET /members": () => problem(401, "unauthenticated"),
      },
    });
    const { router } = renderApp("/inbox", { api });
    await screen.findByRole("heading", { level: 1, name: "Диалоги" });
    alive = false;
    await router.navigate({ to: "/settings/team" });
    expect(await screen.findByRole("heading", { level: 1, name: "Вход" })).toBeInTheDocument();
    expect(router.state.location.search).toEqual({ redirect: "/settings/team" });
  });
});

describe("login form", () => {
  it("has labelled fields with autofill hints and links to sign-up", async () => {
    renderApp("/login", { api: signedOut() });
    expect(await screen.findByRole("heading", { level: 1, name: "Вход" })).toBeInTheDocument();
    expect(screen.getByLabelText("Эл. почта")).toHaveAttribute("autocomplete", "username");
    expect(screen.getByLabelText("Пароль")).toHaveAttribute("autocomplete", "current-password");
    expect(screen.getByRole("link", { name: "Создать пространство" })).toHaveAttribute("href", "/register");
  });

  it("checks the fields before sending and focuses the first mistake", async () => {
    const user = userEvent.setup();
    const api = signedOut();
    renderApp("/login", { api });
    await screen.findByRole("heading", { level: 1, name: "Вход" });

    await user.type(screen.getByLabelText("Пароль"), "x");
    await user.click(screen.getByRole("button", { name: "Войти" }));

    const email = screen.getByLabelText("Эл. почта");
    expect(email).toHaveAttribute("aria-invalid", "true");
    expect(email).toHaveAccessibleDescription("Заполните поле.");
    expect(email).toHaveFocus();
    expect(api.calls.some((c) => c.path === "/auth/login")).toBe(false);
  });

  it.each([
    [problem(401, "invalid_credentials"), "Неверная почта или пароль."],
    [problem(429, "too_many_requests", { "Retry-After": "30" }), "Слишком много попыток. Повторите через 30 секунд."],
    [problem(500, "internal"), "Не получилось. Повторите попытку."],
  ])("reports a failed sign-in in the alert region (%#)", async (response, message) => {
    const user = userEvent.setup();
    renderApp("/login", { api: fakeApi({ session: null, routes: { "POST /auth/login": () => response } }) });
    await screen.findByRole("heading", { level: 1, name: "Вход" });

    await user.type(screen.getByLabelText("Эл. почта"), "anna@zerno.test");
    await user.type(screen.getByLabelText("Пароль"), "wrong-password");
    await user.click(screen.getByRole("button", { name: "Войти" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(message);
  });

  it("reports a network failure", async () => {
    const user = userEvent.setup();
    const api = fakeApi({
      session: null,
      routes: { "POST /auth/login": () => Promise.reject(new TypeError("Failed to fetch")) },
    });
    renderApp("/login", { api });
    await screen.findByRole("heading", { level: 1, name: "Вход" });
    await user.type(screen.getByLabelText("Эл. почта"), "anna@zerno.test");
    await user.type(screen.getByLabelText("Пароль"), "secret-pass");
    await user.click(screen.getByRole("button", { name: "Войти" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Нет связи с сервером");
  });
});

describe("registration", () => {
  async function fillRegistration(user: ReturnType<typeof userEvent.setup>) {
    await user.type(screen.getByLabelText("Ваше имя"), "Анна");
    await user.type(screen.getByLabelText("Название компании"), "Зерно Кофе");
    await user.type(screen.getByLabelText("Эл. почта"), "anna@zerno.test");
    await user.type(screen.getByLabelText("Пароль"), "long-enough");
  }

  it("suggests the address from the company name and opens onboarding", async () => {
    const user = userEvent.setup();
    const api = fakeApi({ session: null, routes: { "POST /auth/register": () => json(201, ownerSession) } });
    const { router } = renderApp("/register", { api });
    await screen.findByRole("heading", { level: 1, name: "Новое пространство" });

    await fillRegistration(user);
    expect(screen.getByLabelText("Адрес пространства")).toHaveValue("zerno-kofe");
    await user.click(screen.getByRole("button", { name: "Создать пространство" }));

    expect(await screen.findByRole("heading", { level: 1, name: "Начало работы" })).toBeInTheDocument();
    expect(router.state.location.pathname).toBe("/onboarding");
    expect(api.calls.find((c) => c.path === "/auth/register")?.body).toMatchObject({
      name: "Анна",
      workspace_name: "Зерно Кофе",
      workspace_slug: "zerno-kofe",
      email: "anna@zerno.test",
      password: "long-enough",
      locale: "ru",
    });
  });

  it("shows a taken address at its field", async () => {
    const user = userEvent.setup();
    const api = fakeApi({ session: null, routes: { "POST /auth/register": () => problem(409, "slug_taken") } });
    renderApp("/register", { api });
    await screen.findByRole("heading", { level: 1, name: "Новое пространство" });

    await fillRegistration(user);
    await user.click(screen.getByRole("button", { name: "Создать пространство" }));

    const slug = screen.getByLabelText("Адрес пространства");
    await waitFor(() => expect(slug).toHaveAttribute("aria-invalid", "true"));
    expect(slug).toHaveAccessibleDescription(/Этот адрес уже занят/);
    expect(slug).toHaveFocus();
  });

  it("rejects a short password before sending", async () => {
    const user = userEvent.setup();
    const api = signedOut();
    renderApp("/register", { api });
    await screen.findByRole("heading", { level: 1, name: "Новое пространство" });

    await fillRegistration(user);
    await user.clear(screen.getByLabelText("Пароль"));
    await user.type(screen.getByLabelText("Пароль"), "short");
    await user.click(screen.getByRole("button", { name: "Создать пространство" }));

    expect(screen.getByLabelText("Пароль")).toHaveAccessibleDescription(/от 8 до 128 символов/);
    expect(api.calls.some((c) => c.path === "/auth/register")).toBe(false);
  });

  it("is outside the panel shell", async () => {
    renderApp("/register", { api: signedOut() });
    await screen.findByRole("heading", { level: 1, name: "Новое пространство" });
    expect(screen.queryByRole("navigation", { name: "Разделы" })).not.toBeInTheDocument();
  });
});
