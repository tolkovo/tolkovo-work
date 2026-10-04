import { createFileRoute } from "@tanstack/react-router";

import { AppShell } from "../components/shell/app-shell";

// Pathless layout: every panel section renders inside the shell.
// P-02 adds the auth guard here (beforeLoad → /login when there is no session).
export const Route = createFileRoute("/_app")({
  component: AppShell,
});
