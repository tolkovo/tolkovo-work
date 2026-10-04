import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { InvitationPreview } from "@tolkovo/api-client";
import { describe, expect, it } from "vitest";

import { fakeApi, json, operatorSession, ownerSession, problem } from "../../test/fake-api";
import { renderApp } from "../../test/render-app";

const empty = (status: number) => () => new Response(null, { status });

const newcomer: InvitationPreview = {
  workspace_name: "Зерно",
  email: "olga@zerno.test",
  role: "operator",
  expires_at: "2026-10-11T10:00:00Z",
  account_exists: false,
};

describe("forgot password", () => {
  it("is linked from sign-in and answers the same for any address", async () => {
    const user = userEvent.setup();
    const api = fakeApi({ session: null, routes: { "POST /auth/password/forgot": empty(202) } });
    const { router } = renderApp("/login", { api });
    await user.click(await screen.findByRole("link", { name: "Забыли пароль?" }));
    expect(await screen.findByRole("heading", { level: 1, name: "Восстановление пароля" })).toBeInTheDocument();
    expect(router.state.location.pathname).toBe("/forgot-password");

    await user.type(screen.getByLabelText("Эл. почта"), "anna@zerno.test");
    await user.click(screen.getByRole("button", { name: "Отправить ссылку" }));

    expect(await screen.findByText(/Если аккаунт с этой почтой есть, письмо уже в пути/)).toBeInTheDocument();
    expect(api.calls.find((c) => c.path === "/auth/password/forgot")?.body).toEqual({ email: "anna@zerno.test" });
  });

  it("explains when the server cannot send email", async () => {
    const user = userEvent.setup();
    const api = fakeApi({ session: null, routes: { "POST /auth/password/forgot": () => problem(503, "email_disabled") } });
    renderApp("/forgot-password", { api });
    await user.type(await screen.findByLabelText("Эл. почта"), "anna@zerno.test");
    await user.click(screen.getByRole("button", { name: "Отправить ссылку" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("На сервере не настроена отправка почты");
  });
});

describe("reset password", () => {
  it("takes the token from the fragment, hides it and sets the new password", async () => {
    const user = userEvent.setup();
    const api = fakeApi({ session: null, routes: { "POST /auth/password/reset": empty(204) } });
    const { router } = renderApp("/reset-password#tok-reset", { api });
    expect(await screen.findByRole("heading", { level: 1, name: "Новый пароль" })).toBeInTheDocument();
    await waitFor(() => expect(router.state.location.hash).toBe(""));

    await user.type(screen.getByLabelText("Новый пароль"), "brand new pass");
    await user.type(screen.getByLabelText("Повторите пароль"), "brand new typo");
    await user.click(screen.getByRole("button", { name: "Сохранить пароль" }));
    expect(screen.getByLabelText("Повторите пароль")).toHaveAccessibleDescription("Пароли не совпадают.");
    expect(api.calls.some((c) => c.path === "/auth/password/reset")).toBe(false);

    await user.clear(screen.getByLabelText("Повторите пароль"));
    await user.type(screen.getByLabelText("Повторите пароль"), "brand new pass");
    await user.click(screen.getByRole("button", { name: "Сохранить пароль" }));

    expect(await screen.findByText("Пароль изменён. Войдите с новым паролем.")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Перейти ко входу" })).toHaveAttribute("href", "/login");
    expect(api.calls.find((c) => c.path === "/auth/password/reset")?.body).toEqual({
      token: "tok-reset",
      password: "brand new pass",
    });
  });

  it("offers a new link when this one is used up", async () => {
    const user = userEvent.setup();
    const api = fakeApi({ session: null, routes: { "POST /auth/password/reset": () => problem(422, "invalid_token") } });
    renderApp("/reset-password#tok-old", { api });
    await user.type(await screen.findByLabelText("Новый пароль"), "brand new pass");
    await user.type(screen.getByLabelText("Повторите пароль"), "brand new pass");
    await user.click(screen.getByRole("button", { name: "Сохранить пароль" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Ссылка недействительна");
    expect(screen.getByRole("link", { name: "Запросить новую ссылку" })).toHaveAttribute("href", "/forgot-password");
  });

  it("explains a link without a token", async () => {
    renderApp("/reset-password", { api: fakeApi({ session: null }) });
    expect(await screen.findByText(/В адресе нет ключа ссылки/)).toBeInTheDocument();
  });
});

describe("invitation", () => {
  it("creates an account and opens the inbox of the new workspace", async () => {
    const user = userEvent.setup();
    const api = fakeApi({
      session: null,
      routes: {
        "POST /auth/invitation/preview": () => json(200, newcomer),
        "POST /auth/invitation/accept": () => json(200, operatorSession),
      },
    });
    const { router } = renderApp("/invite#tok-invite", { api });
    expect(await screen.findByText("Вас приглашают в «Зерно». Роль: Оператор.")).toBeInTheDocument();
    expect(screen.getByLabelText("Эл. почта приглашения")).toHaveValue("olga@zerno.test");
    expect(api.calls.find((c) => c.path === "/auth/invitation/preview")?.body).toEqual({ token: "tok-invite" });

    await user.click(screen.getByRole("button", { name: "Принять приглашение" }));
    expect(screen.getByLabelText("Ваше имя")).toHaveAccessibleDescription(/Заполните поле/);

    await user.type(screen.getByLabelText("Ваше имя"), "Ольга");
    await user.type(screen.getByLabelText("Придумайте пароль"), "operator pass");
    await user.click(screen.getByRole("button", { name: "Принять приглашение" }));

    expect(await screen.findByRole("heading", { level: 1, name: "Диалоги" })).toBeInTheDocument();
    expect(router.state.location.pathname).toBe("/inbox");
    expect(api.calls.find((c) => c.path === "/auth/invitation/accept")?.body).toEqual({
      token: "tok-invite",
      name: "Ольга",
      password: "operator pass",
      locale: "ru",
    });
  });

  it("asks an existing account for its password only", async () => {
    const user = userEvent.setup();
    const api = fakeApi({
      session: null,
      routes: {
        "POST /auth/invitation/preview": () => json(200, { ...newcomer, account_exists: true }),
        "POST /auth/invitation/accept": () => problem(401, "invalid_credentials"),
      },
    });
    renderApp("/invite#tok-invite", { api });
    expect(await screen.findByText(/С этой почтой уже есть аккаунт Tolkovo/)).toBeInTheDocument();
    expect(screen.queryByLabelText("Ваше имя")).not.toBeInTheDocument();
    expect(screen.getByLabelText("Пароль")).toHaveAttribute("autocomplete", "current-password");

    await user.type(screen.getByLabelText("Пароль"), "wrong");
    await user.click(screen.getByRole("button", { name: "Принять приглашение" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Неверная почта или пароль.");
    expect(api.calls.find((c) => c.path === "/auth/invitation/accept")?.body).toEqual({
      token: "tok-invite",
      password: "wrong",
    });
  });

  it("says when the invitation is no longer valid", async () => {
    const api = fakeApi({ session: null, routes: { "POST /auth/invitation/preview": () => problem(422, "invalid_token") } });
    renderApp("/invite#tok-gone", { api });
    expect(await screen.findByText(/Приглашение недействительно/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Принять приглашение" })).not.toBeInTheDocument();
  });

  it("opens for someone already signed in", async () => {
    const api = fakeApi({ session: ownerSession, routes: { "POST /auth/invitation/preview": () => json(200, newcomer) } });
    const { router } = renderApp("/invite#tok-invite", { api });
    expect(await screen.findByRole("button", { name: "Принять приглашение" })).toBeInTheDocument();
    expect(router.state.location.pathname).toBe("/invite");
  });
});

describe("email confirmation", () => {
  it("confirms once as soon as the link opens", async () => {
    const api = fakeApi({ session: null, routes: { "POST /auth/email/verify": empty(204) } });
    renderApp("/verify-email#tok-verify", { api });
    expect(await screen.findByText(/Почта подтверждена/)).toBeInTheDocument();
    expect(api.calls.filter((c) => c.path === "/auth/email/verify")).toEqual([
      expect.objectContaining({ body: { token: "tok-verify" } }),
    ]);
  });

  it("explains a used link", async () => {
    const api = fakeApi({ session: null, routes: { "POST /auth/email/verify": () => problem(422, "invalid_token") } });
    renderApp("/verify-email#tok-old", { api });
    expect(await screen.findByRole("alert")).toHaveTextContent("Отправьте письмо снова в профиле");
  });
});
