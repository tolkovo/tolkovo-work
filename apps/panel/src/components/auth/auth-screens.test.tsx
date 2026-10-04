import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { renderApp } from "../../test/render-app";

describe("auth screens (markup only in P-01)", () => {
  it("login has labelled fields with autofill hints and links to sign-up", async () => {
    renderApp("/login");
    expect(await screen.findByRole("heading", { level: 1, name: "Вход" })).toBeInTheDocument();
    expect(screen.getByLabelText("Эл. почта")).toHaveAttribute("autocomplete", "username");
    expect(screen.getByLabelText("Пароль")).toHaveAttribute("autocomplete", "current-password");
    expect(screen.getByRole("button", { name: "Войти" })).toHaveAttribute("type", "submit");
    expect(screen.getByRole("link", { name: "Создать пространство" })).toHaveAttribute("href", "/register");
  });

  it("register has the workspace fields and switches to login", async () => {
    const user = userEvent.setup();
    const { router } = renderApp("/register");
    expect(await screen.findByRole("heading", { level: 1, name: "Новое пространство" })).toBeInTheDocument();
    for (const label of ["Ваше имя", "Название компании", "Эл. почта", "Пароль"]) {
      expect(screen.getByLabelText(label)).toBeRequired();
    }
    expect(screen.getByLabelText("Пароль")).toHaveAttribute("autocomplete", "new-password");

    await user.click(screen.getByRole("link", { name: "Войти" }));
    expect(await screen.findByRole("heading", { level: 1, name: "Вход" })).toBeInTheDocument();
    expect(router.state.location.pathname).toBe("/login");
  });

  it("auth screens are outside the panel shell", async () => {
    renderApp("/login");
    await screen.findByRole("heading", { level: 1, name: "Вход" });
    expect(screen.queryByRole("navigation", { name: "Разделы" })).not.toBeInTheDocument();
  });
});
