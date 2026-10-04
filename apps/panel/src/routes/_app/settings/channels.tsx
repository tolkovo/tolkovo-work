import { createFileRoute } from "@tanstack/react-router";

import { ChannelsSettings } from "../../../components/settings/channels";

export const Route = createFileRoute("/_app/settings/channels")({
  component: ChannelsSettings,
});
