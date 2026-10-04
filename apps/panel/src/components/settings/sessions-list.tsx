import { isApiError, type DeviceSession } from "@tolkovo/api-client";
import { useMySessions, useRevokeOtherSessions, useRevokeSession } from "@tolkovo/api-client/react";
import { Button, Skeleton } from "@tolkovo/ui";
import { Laptop } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";

import { describeApiError } from "../../lib/api-errors";
import { deviceLabel } from "../../lib/user-agent";
import { FormMessage } from "../form/fields";

/**
 * Signed-in devices of the user (GET /me/sessions): end one, or every other at once. The
 * current session is ended by "Sign out", so it has no button here.
 */
export function SessionsList() {
  const { t } = useTranslation();
  const sessions = useMySessions();
  const revoke = useRevokeSession();
  const revokeOthers = useRevokeOtherSessions();
  const [notice, setNotice] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const onError = (e: unknown) =>
    // 404: someone ended it a moment ago (another tab or device); the list refetches anyway.
    setError(isApiError(e) && e.code === "not_found" ? t("settings.security.sessions.gone") : describeApiError(t, e).message);
  const start = () => {
    setNotice(null);
    setError(null);
  };

  const busy = revoke.isPending || revokeOthers.isPending;
  const others = sessions.data?.filter((s) => !s.current) ?? [];

  return (
    <section aria-labelledby="settings-sessions-title" aria-busy={sessions.isPending} className="flex max-w-[640px] flex-col gap-3">
      <div className="flex flex-col gap-1">
        <h3 id="settings-sessions-title" className="m-0 text-sm font-semibold">
          {t("settings.security.sessions.title")}
        </h3>
        <p className="m-0 text-[13px] text-ink-2">{t("settings.security.sessions.text")}</p>
      </div>
      <FormMessage tone="error" message={error} />
      <FormMessage tone="success" message={notice} />
      {sessions.isPending ? (
        <div role="status" className="flex flex-col gap-2">
          <span className="sr-only">{t("app.loading")}</span>
          <Skeleton className="h-14" />
          <Skeleton className="h-14" />
        </div>
      ) : sessions.isError ? (
        <div role="alert" className="flex flex-col items-start gap-2">
          <p className="m-0 text-ink-2">{describeApiError(t, sessions.error).message}</p>
          <Button variant="secondary" onClick={() => void sessions.refetch()}>
            {t("error.retry")}
          </Button>
        </div>
      ) : (
        <>
          <ul aria-labelledby="settings-sessions-title" className="m-0 flex list-none flex-col divide-y divide-line rounded-xl border border-line bg-surface p-0">
            {sessions.data.map((s) => (
              <SessionRow
                key={s.id}
                session={s}
                disabled={busy}
                onRevoke={() => {
                  start();
                  revoke.mutate(s.id, {
                    onSuccess: () => setNotice(t("settings.security.sessions.revoked")),
                    onError,
                  });
                }}
              />
            ))}
          </ul>
          {others.length > 0 ? (
            <div>
              <Button
                variant="secondary"
                disabled={busy}
                onClick={() => {
                  start();
                  revokeOthers.mutate(undefined, {
                    onSuccess: () => setNotice(t("settings.security.sessions.revokedOthers")),
                    onError,
                  });
                }}
              >
                {t("settings.security.sessions.revokeOthers")}
              </Button>
            </div>
          ) : (
            <p className="m-0 text-[13px] text-ink-3">{t("settings.security.sessions.none")}</p>
          )}
        </>
      )}
    </section>
  );
}

type SessionRowProps = {
  session: DeviceSession;
  disabled: boolean;
  onRevoke: () => void;
};

function SessionRow({ session, disabled, onRevoke }: SessionRowProps) {
  const { t, i18n } = useTranslation();
  const device = deviceLabel(session.user_agent) ?? t("settings.security.sessions.unknownDevice");
  const format = new Intl.DateTimeFormat(i18n.language, { dateStyle: "medium", timeStyle: "short" });
  const date = (iso: string) => format.format(new Date(iso));
  return (
    <li className="flex items-center gap-3 px-3 py-2.5 max-nav:flex-wrap">
      <Laptop aria-hidden="true" className="size-5 shrink-0 text-ink-3" strokeWidth={1.7} />
      <div className="flex min-w-0 flex-1 flex-col gap-0.5">
        <p className="m-0 flex flex-wrap items-center gap-2 text-[13px] font-medium text-ink">
          <span className="truncate" title={session.user_agent || undefined}>
            {device}
          </span>
          {session.current ? (
            <span className="rounded-full border border-teal bg-teal-soft px-2 py-px text-[11.5px] font-medium text-ink">
              {t("settings.security.sessions.current")}
            </span>
          ) : null}
        </p>
        <p className="m-0 flex flex-wrap gap-x-3 text-[12.5px] text-ink-3">
          <span>{t("settings.security.sessions.signedIn", { date: date(session.created_at) })}</span>
          <span>{t("settings.security.sessions.lastSeen", { date: date(session.last_seen_at) })}</span>
        </p>
      </div>
      {session.current ? null : (
        <Button
          variant="ghost"
          size="sm"
          disabled={disabled}
          aria-label={t("settings.security.sessions.revokeLabel", { device })}
          onClick={onRevoke}
        >
          {t("settings.security.sessions.revoke")}
        </Button>
      )}
    </li>
  );
}

