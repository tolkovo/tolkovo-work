import { Outlet, createRootRouteWithContext } from "@tanstack/react-router";

import { ErrorView } from "../components/error-view";
import { NotFound } from "../components/not-found";
import type { RouterContext } from "../router";

export const Route = createRootRouteWithContext<RouterContext>()({
  component: Outlet,
  // Errors outside any section (e.g. in the shell itself) replace the whole screen.
  errorComponent: RootError,
  notFoundComponent: NotFound,
});

function RootError({ reset }: { reset: () => void }) {
  return (
    <main className="grid min-h-dvh place-items-center bg-paper p-4">
      <ErrorView reset={reset} />
    </main>
  );
}
