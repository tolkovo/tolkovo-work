import { useCan, useChannels } from "@tolkovo/api-client/react";
import { Link } from "@tanstack/react-router";
import { AlertTriangle } from "lucide-react";
import { useTranslation } from "react-i18next";

import { channelError } from "../../lib/channels/errors";

/** How often the shell checks the channels of admins (a broken one stops customers' messages). */
export const CHANNEL_ALERT_POLL_MS = 60_000;

/**
 * Tells those who manage channels that one stopped working (docs/02 §11.4): a revoked bot token
 * or an ended Telegram session cuts customers off until an admin reconnects it. The wrapper
 * always renders, so the shell's grid keeps its rows.
 */
export function ChannelAlert() {
  const { t } = useTranslation();
  const canView = useCan("channels.view");
  const channels = useChannels({ enabled: canView, poll: () => (canView ? CHANNEL_ALERT_POLL_MS : false) });
  const broken = canView ? (channels.data?.items ?? []).filter((c) => c.status === "error") : [];
  const first = broken[0];
  return (
    <div>
      {first && (
        <div
          role="alert"
          className="flex flex-wrap items-center gap-x-3 gap-y-1 border-b border-danger/30 bg-danger-soft px-4 py-2 text-[13px] text-ink max-nav:px-3"
        >
          <AlertTriangle aria-hidden="true" className="size-4 shrink-0 text-danger" />
          <span className="min-w-0 flex-1 break-words max-nav:line-clamp-2">
            {broken.length === 1
              ? t("channelAlert.one", { name: first.name, reason: channelError(t, first.error_code) })
              : t("channelAlert.many", { count: broken.length })}
          </span>
          <Link to="/settings/channels" className="font-medium text-danger underline underline-offset-2">
            {t("channelAlert.open")}
          </Link>
        </div>
      )}
    </div>
  );
}
