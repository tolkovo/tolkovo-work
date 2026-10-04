import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { Invitation } from "@tolkovo/api-client";
import { describe, expect, it } from "vitest";

import { fakeApi, json, operatorSession, ownerSession, problem } from "../../test/fake-api";
import { renderApp } from "../../test/render-app";

const open: Invitation = {
  id: "inv-1",
  email: "olga@zerno.test",
  role: "operator",
  invited_by_name: "Анна Смирнова",
  expires_at: "2026-10-11T10:00:00Z",
  created_at: "2026-10-04T10:00:00Z",
  expired: false,
};

describe("team invitations", () => {
  it("invites by email with the chosen role and confirms the email went out", async () => {
    const user = userEvent.setup();
    const api = fakeApi({
      routes: {
        "POST /invitations": () => json(201, { invitation: { ...open, role: "admin", email: "ivan@zerno.test" } }),
      },
    });
    renderApp("/settings/team", { api });
    const email = await screen.findByLabelText("Эл. почта", { selector: "#invite-email" });

    await user.type(email, "ivan@zerno.test");
    await user.selectOptions(screen.getByLabelText("Роль"), "admin");
    await user.click(screen.getByRole("button", { name: "Пригласить" }));

    expect(await screen.findByText("Приглашение отправлено на ivan@zerno.test. Ссылка действует 7 дней.")).toBeInTheDocument();
    const call = api.calls.find((c) => c.method === "POST" && c.path === "/invitations");
    expect(call?.body).toEqual({ email: "ivan@zerno.test", role: "admin" });
    expect(call?.headers.get("X-CSRF-Token")).toBe("csrf-owner");
    expect(email).toHaveValue("");
  });

  it("shows the link once when the server cannot send email", async () => {
    const user = userEvent.setup();
    const url = "http://panel.test/invite#tok";
    const api = fakeApi({ routes: { "POST /invitations": () => json(201, { invitation: open, invite_url: url }) } });
    renderApp("/settings/team", { api });

    await user.type(await screen.findByLabelText("Эл. почта", { selector: "#invite-email" }), "olga@zerno.test");
    await user.click(screen.getByRole("button", { name: "Пригласить" }));

    expect(await screen.findByLabelText("Ссылка приглашения")).toHaveValue(url);
    expect(screen.getByText(/Передайте ссылку для olga@zerno.test сами/)).toBeInTheDocument();
  });

  it("puts an existing member error at the email field", async () => {
    const user = userEvent.setup();
    const api = fakeApi({ routes: { "POST /invitations": () => problem(409, "already_member") } });
    renderApp("/settings/team", { api });
    const email = await screen.findByLabelText("Эл. почта", { selector: "#invite-email" });
    await user.type(email, "petr@zerno.test");
    await user.click(screen.getByRole("button", { name: "Пригласить" }));
    expect(await screen.findByText("Этот человек уже в команде.")).toBeInTheDocument();
    expect(email).toHaveAttribute("aria-invalid", "true");
    expect(email).toHaveFocus();
  });

  it("lists open invitations and resends or revokes them", async () => {
    const user = userEvent.setup();
    let items = [open, { ...open, id: "inv-2", email: "old@zerno.test", expired: true }];
    const api = fakeApi({
      routes: {
        "GET /invitations": () => json(200, { items, email_enabled: true }),
        "POST /invitations/inv-2/resend": () => json(200, { invitation: { ...items[1], expired: false } }),
        "DELETE /invitations/inv-1": () => {
          items = items.filter((i) => i.id !== "inv-1");
          return new Response(null, { status: 204 });
        },
      },
    });
    renderApp("/settings/team", { api });
    const list = await screen.findByRole("list", { name: "Приглашения" });
    expect(within(list).getByText("Срок истёк")).toBeInTheDocument();
    expect(within(list).getAllByText("Пригласил(а): Анна Смирнова")).toHaveLength(2);

    await user.click(screen.getByRole("button", { name: "Отправить приглашение снова: old@zerno.test" }));
    expect(await screen.findByText("Приглашение для old@zerno.test отправлено снова.")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Отозвать приглашение: olga@zerno.test" }));
    expect(await screen.findByText("Приглашение для olga@zerno.test отозвано.")).toBeInTheDocument();
    expect(await screen.findByRole("list", { name: "Приглашения" })).not.toHaveTextContent("olga@zerno.test");
  });

  it("offers a new link instead of an email when the server cannot send one", async () => {
    const api = fakeApi({ routes: { "GET /invitations": () => json(200, { items: [open], email_enabled: false }) } });
    renderApp("/settings/team", { api });
    expect(await screen.findByRole("button", { name: "Новая ссылка приглашения: olga@zerno.test" })).toBeInTheDocument();
  });

  it("is hidden from an operator, who cannot manage members", async () => {
    const api = fakeApi({ session: operatorSession });
    renderApp("/settings/team", { api });
    expect(await screen.findByRole("heading", { level: 2, name: "Команда" })).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Пригласить в команду" })).not.toBeInTheDocument();
    expect(api.calls.some((c) => c.path === "/invitations")).toBe(false);
  });
});

describe("email confirmation in the profile", () => {
  it("offers to send the confirmation email again while the address is unconfirmed", async () => {
    const user = userEvent.setup();
    const api = fakeApi({
      session: { ...ownerSession, user: { ...ownerSession.user, email_verified: false } },
      routes: { "POST /me/email/verification": () => new Response(null, { status: 202 }) },
    });
    renderApp("/settings/profile", { api });
    expect(await screen.findByText(/Почта не подтверждена/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Отправить письмо для подтверждения" }));
    expect(await screen.findByText("Письмо отправлено. Ссылка действует 48 часов.")).toBeInTheDocument();
    // The button is not the profile form's submit.
    expect(api.calls.some((c) => c.method === "PATCH")).toBe(false);
  });

  it("stays quiet for a confirmed address", async () => {
    renderApp("/settings/profile");
    expect(await screen.findByLabelText("Имя")).toBeInTheDocument();
    expect(screen.queryByText(/Почта не подтверждена/)).not.toBeInTheDocument();
  });
});
