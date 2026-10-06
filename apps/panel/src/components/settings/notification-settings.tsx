import type { EmailDelayMinutes, NotificationSettingsInput } from "@tolkovo/api-client";
import { useCan, useNotificationSettings, useSession, useUpdateNotificationSettings } from "@tolkovo/api-client/react";
import { Button, Skeleton, cn } from "@tolkovo/ui";
import { Link } from "@tanstack/react-router";
import { Volume2 } from "lucide-react";
import { useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";

import { describeApiError } from "../../lib/api-errors";
import { currentNotifier } from "../../lib/notifications/instance";
import {
  ALERT_KINDS,
  desktopPermission,
  useNotifySettings,
  type AlertKind,
  type DesktopPermission,
} from "../../lib/notifications/settings";
import { createSoundPlayer } from "../../lib/notifications/sound";
import { FormMessage, SelectField } from "../form/fields";
import { TelegramLink } from "./telegram-link";

const EMAIL_DELAYS: readonly EmailDelayMinutes[] = [5, 15, 30, 60];

/** One player for the «Прослушать» buttons of the page. */
const preview = createSoundPlayer();

function Checkbox({
  id,
  checked,
  disabled,
  onChange,
  label,
  hint,
  children,
}: {
  id: string;
  checked: boolean;
  disabled?: boolean;
  onChange: (checked: boolean) => void;
  label: string;
  hint?: string;
  children?: ReactNode;
}) {
  return (
    <div className="flex items-start gap-2.5">
      <input
        id={id}
        type="checkbox"
        checked={checked}
        disabled={disabled}
        onChange={(e) => onChange(e.target.checked)}
        aria-describedby={hint ? `${id}-hint` : undefined}
        className="mt-0.5 size-4 shrink-0 accent-teal"
      />
      <div className="flex min-w-0 flex-1 flex-col gap-0.5">
        <label htmlFor={id} className="text-[13px] text-ink">
          {label}
        </label>
        {hint ? (
          <p id={`${id}-hint`} className="m-0 text-[12.5px] text-ink-3">
            {hint}
          </p>
        ) : null}
      </div>
      {children}
    </div>
  );
}

/** «На этом устройстве»: desktop notifications and sounds of this browser. */
function DeviceSettings() {
  const { t } = useTranslation();
  const settings = useNotifySettings();
  const [permission, setPermission] = useState<DesktopPermission>(desktopPermission);
  const [tested, setTested] = useState(false);

  const ask = async () => {
    try {
      setPermission(await Notification.requestPermission());
    } catch {
      setPermission(desktopPermission());
    }
  };

  return (
    <section aria-labelledby="notify-device-title" className="flex max-w-[640px] flex-col gap-4">
      <div className="flex flex-col gap-1">
        <h3 id="notify-device-title" className="m-0 text-sm font-semibold">
          {t("settings.notifications.device.title")}
        </h3>
        <p className="m-0 text-[13px] text-ink-2">{t("settings.notifications.device.text")}</p>
      </div>

      <div className="flex flex-col gap-2 rounded-xl border border-line bg-surface p-4">
        <h4 className="m-0 text-[13px] font-semibold">{t("settings.notifications.desktop.title")}</h4>
        <p role="status" className="m-0 text-[13px] text-ink-2">
          {t(`settings.notifications.desktop.${permission}`)}
        </p>
        {permission === "default" ? (
          <div>
            <Button variant="secondary" onClick={() => void ask()}>
              {t("settings.notifications.desktop.allow")}
            </Button>
          </div>
        ) : null}
        {permission === "granted" ? (
          <Checkbox
            id="notify-desktop"
            checked={settings.desktop}
            onChange={settings.setDesktop}
            label={t("settings.notifications.desktop.show")}
            hint={t("settings.notifications.desktop.hint")}
          />
        ) : null}
      </div>

      <fieldset className="m-0 flex flex-col gap-3 rounded-xl border border-line bg-surface p-4">
        <legend className="float-left mb-1 p-0 text-[13px] font-semibold">{t("settings.notifications.sounds.title")}</legend>
        {ALERT_KINDS.map((kind: AlertKind) => (
          <Checkbox
            key={kind}
            id={`notify-sound-${kind}`}
            checked={settings.sounds[kind]}
            onChange={(on) => settings.setSound(kind, on)}
            label={t(`settings.notifications.sounds.${kind}`)}
          >
            <Button
              variant="ghost"
              size="sm"
              className="shrink-0 gap-1"
              aria-label={t("settings.notifications.sounds.playLabel", { sound: t(`settings.notifications.sounds.${kind}`) })}
              onClick={() => preview.play(kind, settings.volume)}
            >
              <Volume2 aria-hidden="true" className="size-3.5" strokeWidth={1.8} />
              {t("settings.notifications.sounds.play")}
            </Button>
          </Checkbox>
        ))}
        <div className="flex flex-wrap items-center gap-3">
          <label htmlFor="notify-volume" className="text-[13px] text-ink">
            {t("settings.notifications.sounds.volume")}
          </label>
          <input
            id="notify-volume"
            type="range"
            min={0}
            max={100}
            step={10}
            value={Math.round(settings.volume * 100)}
            onChange={(e) => settings.setVolume(Number(e.target.value) / 100)}
            className="w-48 max-w-full accent-teal"
          />
          <span aria-hidden="true" className="text-[12.5px] text-ink-3 tabular-nums">
            {Math.round(settings.volume * 100)}%
          </span>
        </div>
        <p className="m-0 text-[12.5px] text-ink-3">{t("settings.notifications.sounds.hint")}</p>
      </fieldset>

      <div className="flex flex-col items-start gap-2">
        <Button
          variant="secondary"
          onClick={() => {
            currentNotifier()?.test();
            setTested(true);
          }}
        >
          {t("settings.notifications.test.send")}
        </Button>
        <p role="status" className={cn("m-0 text-[12.5px] text-ink-3", !tested && "sr-only")}>
          {tested ? t("settings.notifications.test.sent") : ""}
        </p>
      </div>
    </section>
  );
}

/** «На почту»: the email about missed messages, kept on the server. */
function EmailSettings() {
  const { t } = useTranslation();
  const { data: session } = useSession();
  const query = useNotificationSettings();
  const update = useUpdateNotificationSettings();
  const [saved, setSaved] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const save = (body: NotificationSettingsInput) => {
    setSaved(null);
    setError(null);
    update.mutate(body, {
      onSuccess: () => setSaved(t("settings.notifications.email.saved")),
      onError: (e) => setError(describeApiError(t, e).message),
    });
  };

  const email = session?.user.email ?? "";
  return (
    <section
      aria-labelledby="notify-email-title"
      aria-busy={query.isPending}
      className="flex max-w-[640px] flex-col gap-3"
    >
      <h3 id="notify-email-title" className="m-0 text-sm font-semibold">
        {t("settings.notifications.email.title")}
      </h3>
      {query.isPending ? (
        <div role="status">
          <span className="sr-only">{t("app.loading")}</span>
          <Skeleton className="h-24" />
        </div>
      ) : query.isError ? (
        <div role="alert" className="flex flex-col items-start gap-2">
          <p className="m-0 text-ink-2">{describeApiError(t, query.error).message}</p>
          <Button variant="secondary" onClick={() => void query.refetch()}>
            {t("error.retry")}
          </Button>
        </div>
      ) : (
        <div className="flex flex-col gap-3 rounded-xl border border-line bg-surface p-4">
          {!query.data.email_available ? (
            <p className="m-0 rounded-lg border border-line bg-paper px-3 py-2 text-[13px] text-ink-2">
              {t("settings.notifications.email.unavailable")}
            </p>
          ) : session && !session.user.email_verified ? (
            <p className="m-0 rounded-lg border border-line bg-paper px-3 py-2 text-[13px] text-ink-2">
              {t("settings.notifications.email.unverified", { email })}{" "}
              <Link to="/settings/profile" className="text-teal underline underline-offset-2">
                {t("settings.notifications.email.toProfile")}
              </Link>
            </p>
          ) : null}
          <Checkbox
            id="notify-email-missed"
            checked={query.data.email_missed}
            disabled={update.isPending}
            onChange={(on) => save({ email_missed: on })}
            label={t("settings.notifications.email.missed")}
            hint={t("settings.notifications.email.missedHint", { email })}
          />
          <div className="max-w-[240px]">
            <SelectField
              id="notify-email-delay"
              name="email_delay_minutes"
              label={t("settings.notifications.email.delay")}
              value={String(query.data.email_delay_minutes)}
              disabled={update.isPending || !query.data.email_missed}
              onChange={(e) => save({ email_delay_minutes: Number(e.target.value) as EmailDelayMinutes })}
            >
              {EMAIL_DELAYS.map((m) => (
                <option key={m} value={m}>
                  {t("settings.notifications.email.minutes", { count: m })}
                </option>
              ))}
            </SelectField>
          </div>
          <FormMessage tone="error" message={error} />
          <FormMessage tone="success" message={saved} />
        </div>
      )}
    </section>
  );
}

/** «Уведомления» of the personal settings (docs/07 §7, §8). */
export function NotificationSettingsPage() {
  const { t } = useTranslation();
  // Telegram carries messages about tasks: members who see the section (TK-02).
  const tasks = useCan("tasks.view");
  return (
    <section aria-labelledby="settings-notifications-title" className="flex flex-col gap-6">
      <h2 id="settings-notifications-title" className="m-0 text-base font-semibold">
        {t("settings.notifications.title")}
      </h2>
      <DeviceSettings />
      {tasks && <TelegramLink />}
      <EmailSettings />
    </section>
  );
}
