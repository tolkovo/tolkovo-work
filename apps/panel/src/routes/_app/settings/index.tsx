import { sessionQueryOptions } from "@tolkovo/api-client/react";
import { createFileRoute, redirect } from "@tanstack/react-router";

import { firstSettingsSection } from "../../../components/settings/sections";

export const Route = createFileRoute("/_app/settings/")({
  // The /_app guard has loaded the session already; this reads it from the cache.
  beforeLoad: async ({ context }) => {
    const session = await context.queryClient.ensureQueryData(sessionQueryOptions(context.api));
    throw redirect({ to: firstSettingsSection(session), replace: true });
  },
});
