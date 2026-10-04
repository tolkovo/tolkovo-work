import { QueryClientProvider } from "@tanstack/react-query";
import {
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
} from "@tanstack/react-router";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nextProvider } from "react-i18next";
import { describe, expect, it, vi } from "vitest";

import { createI18n } from "../lib/i18n";
import { createQueryClient } from "../lib/query-client";
import { ErrorView } from "./error-view";

// A minimal router with a route that fails the first time it renders, then recovers.
function renderFailingRoute() {
  let shouldFail = true;
  function Flaky() {
    if (shouldFail) throw new Error("boom: secret details");
    return <p>recovered</p>;
  }
  const rootRoute = createRootRoute();
  const route = createRoute({ getParentRoute: () => rootRoute, path: "/", component: Flaky });
  const router = createRouter({
    routeTree: rootRoute.addChildren([route]),
    history: createMemoryHistory({ initialEntries: ["/"] }),
    defaultErrorComponent: ErrorView,
  });
  render(
    <I18nextProvider i18n={createI18n("ru")}>
      <QueryClientProvider client={createQueryClient()}>
        <RouterProvider router={router} />
      </QueryClientProvider>
    </I18nextProvider>,
  );
  return { recover: () => (shouldFail = false) };
}

describe("ErrorView", () => {
  it("explains what happened, hides technical details and lets the user retry", async () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    vi.spyOn(console, "warn").mockImplementation(() => {});
    const user = userEvent.setup();
    const { recover } = renderFailingRoute();

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Что-то пошло не так");
    expect(alert).not.toHaveTextContent("secret details");

    recover();
    await user.click(screen.getByRole("button", { name: "Повторить" }));
    expect(await screen.findByText("recovered")).toBeInTheDocument();
  });
});
