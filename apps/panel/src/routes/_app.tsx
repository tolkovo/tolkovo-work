import { sessionQueryOptions } from "@tolkovo/api-client/react";
import { createFileRoute, redirect } from "@tanstack/react-router";

import { ErrorView } from "../components/error-view";
import { AppShell } from "../components/shell/app-shell";

// Pathless layout: every panel section renders inside the shell, and only with a session.
export const Route = createFileRoute("/_app")({
  beforeLoad: async ({ context, location }) => {
    const session = await context.queryClient.ensureQueryData(sessionQueryOptions(context.api));
    if (!session) {
      // Keep where the person was going, so sign-in brings them back there.
      throw redirect({ to: "/login", search: { redirect: location.href }, replace: true });
    }
  },
  component: AppShell,
  // The session check failed (server unreachable): full-screen error, the shell needs a session.
  errorComponent: ({ error, reset }) => (
    <main className="grid min-h-dvh place-items-center bg-paper p-4">
      <ErrorView error={error} reset={reset} />
    </main>
  ),
});
