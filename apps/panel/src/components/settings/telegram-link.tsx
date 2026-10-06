import type { TelegramLinkStart } from "@tolkovo/api-client";
import { useCan, useCheckMyTelegram, useMyTelegram, useStartMyTelegramLink, useUnlinkMyTelegram } from "@tolkovo/api-client/react";
import { Link } from "@tanstack/react-router";
import { Button, Skeleton } from "@tolkovo/ui";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";

import { describeApiError } from "../../lib/api-errors";
import { FormMessage } from "../form/fields";

/**
 * «Telegram» of the personal notifications (TK-02): messages about tasks go to the member's
 * Telegram through the workspace's notifications bot. The member opens the bot with a code and
 * presses Start; the panel asks the server to read the bot's messages until the link is done.
 */
export function TelegramLink() {
  const { t } = useTranslation();
  const me = useMyTelegram();
  const start = useStartMyTelegramLink();
  const unlink = useUnlinkMyTelegram();
  const canManage = useCan("tasks.manage");
  const [link, setLink] = useState<TelegramLinkStart | null>(null);
  // The code expires: the link goes away with it and the button comes back.
  useEffect(() => {
    if (!link) return;
    // Timers take at most 2^31-1 ms; a code lives 15 minutes.
    const timer = setTimeout(() => setLink(null), Math.min(2 ** 31 - 1, Math.max(0, Date.parse(link.expires_at) - Date.now())));
    return () => clearTimeout(timer);
  }, [link]);
  const waiting = link !== null && me.data?.linked !== true;
  const check = useCheckMyTelegram({ enabled: waiting });
  const failure = start.error ?? unlink.error ?? check.error;

  return (
    <section aria-labelledby="notify-telegram-title" aria-busy={me.isPending} className="flex max-w-[640px] flex-col gap-3">
      <h3 id="notify-telegram-title" className="m-0 text-sm font-semibold">
        {t("settings.notifications.telegram.title")}
      </h3>
      {me.isPending ? (
        <Skeleton className="h-16" />
      ) : me.isError ? (
        <p role="alert" className="m-0 text-ink-2">
          {describeApiError(t, me.error).message}
        </p>
      ) : (
        <div className="flex flex-col gap-3 rounded-xl border border-line bg-surface p-4 text-[13px]">
          <p className="m-0 text-ink-2">{t("settings.notifications.telegram.lead")}</p>
          {me.data.bot_username === null ? (
            <p className="m-0 rounded-lg border border-line bg-paper px-3 py-2 text-ink-2">
              {t("settings.notifications.telegram.noBot")}{" "}
              {canManage && (
                <Link to="/settings/tasks" className="text-teal underline underline-offset-2">
                  {t("settings.notifications.telegram.toSettings")}
                </Link>
              )}
            </p>
          ) : me.data.linked ? (
            <div className="flex flex-wrap items-center gap-2">
              <p role="status" className="m-0 mr-auto">
                {t("settings.notifications.telegram.linked", { name: me.data.name ?? "", bot: me.data.bot_username })}
              </p>
              <Button size="sm" disabled={unlink.isPending} onClick={() => unlink.mutate(undefined, { onSuccess: () => setLink(null) })}>
                {t("settings.notifications.telegram.unlink")}
              </Button>
            </div>
          ) : waiting && link ? (
            <div className="flex flex-col items-start gap-2">
              <p className="m-0">{t("settings.notifications.telegram.steps", { bot: me.data.bot_username })}</p>
              <Button asChild variant="primary">
                <a href={link.url} target="_blank" rel="noreferrer">
                  {t("settings.notifications.telegram.open", { bot: me.data.bot_username })}
                </a>
              </Button>
              <p role="status" className="m-0 text-ink-3">
                {t("settings.notifications.telegram.waiting")}
              </p>
            </div>
          ) : (
            <Button
              variant="primary"
              className="self-start"
              disabled={start.isPending}
              onClick={() => start.mutate(undefined, { onSuccess: (l) => setLink(l) })}
            >
              {t("settings.notifications.telegram.link")}
            </Button>
          )}
          <FormMessage tone="error" message={failure ? describeApiError(t, failure).message : null} />
        </div>
      )}
    </section>
  );
}
