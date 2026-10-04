import { sessionQueryOptions } from "@tolkovo/api-client/react";
import { createFileRoute, redirect } from "@tanstack/react-router";

import { AuthLayout } from "../components/auth/auth-layout";
import { HOME_PATH } from "../lib/redirect";

export const Route = createFileRoute("/_auth")({
  beforeLoad: async ({ context }) => {
    // Someone already signed in has nothing to do on the sign-in screens.
    // If the session cannot be checked (offline), show the form anyway: it reports the error.
    const session = await context.queryClient
      .ensureQueryData(sessionQueryOptions(context.api))
      .catch(() => null);
    if (session) throw redirect({ href: HOME_PATH, replace: true });
  },
  component: AuthLayout,
});
