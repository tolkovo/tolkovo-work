import { createFileRoute } from "@tanstack/react-router";

import { AuthLayout } from "../components/auth/auth-layout";

/**
 * Pages behind emailed links: accept an invitation, set a new password, confirm the email.
 * Unlike the sign-in screens they open for signed-in people too: a colleague may accept an
 * invitation into a second workspace, and the confirmation link usually opens mid-session.
 */
export const Route = createFileRoute("/_link")({
  component: AuthLayout,
});
