import type { AppChannelCreated, AppLimits, Channel, IngestKey } from "@tolkovo/api-client";
import {
  useConnectAppChannel,
  useCreateIngestKey,
  useIngestKeys,
  useRevokeIngestKey,
  useRotateIdentitySecret,
  useSetAppLimits,
} from "@tolkovo/api-client/react";
import { Button, Input, Label, Skeleton, cn } from "@tolkovo/ui";
import { useEffect, useId, useRef, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

import { describeApiError } from "../../lib/api-errors";
import { formValues } from "../../lib/forms";
import { FormMessage, TextField } from "../form/fields";

/**
 * The «Приложение» channel (docs/02 §11.6, AP-01): a company's own app sends problem reports.
 * The ingest key goes into the app and can only create reports; the identity secret stays on
 * the company's backend, which signs who the app user is. Both are shown once: the server keeps
 * the key's hash and the secret sealed.
 */

/** A secret shown once, with a copy button; selecting the text works when the clipboard does not. */
function SecretBox({ label, value, hint }: { label: string; value: string; hint: string }) {
  const { t } = useTranslation();
  const id = useId();
  const input = useRef<HTMLInputElement>(null);
  const [copied, setCopied] = useState(false);

  const copy = () => {
    input.current?.select();
    void navigator.clipboard?.writeText(value).then(
      () => setCopied(true),
      () => setCopied(false),
    );
  };

  return (
    <div className="flex flex-col gap-1.5">
      <Label htmlFor={id}>{label}</Label>
      <div className="flex gap-2 max-nav:flex-col">
        <Input
          id={id}
          ref={input}
          value={value}
          readOnly
          spellCheck={false}
          aria-describedby={`${id}-hint`}
          onFocus={(e) => e.currentTarget.select()}
          className="min-w-0 flex-1 font-mono text-[12.5px]"
        />
        <Button variant="secondary" onClick={copy}>
          {t("settings.channels.app.copy")}
        </Button>
      </div>
      <p id={`${id}-hint`} className="m-0 text-[12.5px] text-ink-3">
        {hint}
      </p>
      <FormMessage tone="success" message={copied ? t("settings.channels.app.copied") : null} />
    </div>
  );
}

/** Wizard step: the channel's name, then the server issues the key and the secret. */
export function AppStep({ onBack, onDone }: { onBack: () => void; onDone: (created: AppChannelCreated) => void }) {
  const { t } = useTranslation();
  const connect = useConnectAppChannel();
  const [error, setError] = useState<{ field?: "name"; message: string } | null>(null);

  const onSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const name = (formValues(event.currentTarget).name ?? "").trim();
    setError(null);
    connect.mutate(name, { onSuccess: onDone, onError: (e) => setError(describeApiError<"name">(t, e, { invalid_request: "name" })) });
  };

  return (
    <form onSubmit={onSubmit} noValidate aria-busy={connect.isPending} className="flex flex-col gap-3">
      <p className="m-0 max-w-[600px] text-[13px] text-ink-2">{t("settings.channels.wizard.appStep.text")}</p>
      <FormMessage tone="error" message={error && !error.field ? error.message : null} />
      <fieldset disabled={connect.isPending} className="m-0 flex max-w-[360px] flex-col gap-3 border-0 p-0">
        <TextField
          id="app-name"
          name="name"
          label={t("settings.channels.wizard.appStep.label")}
          hint={t("settings.channels.wizard.appStep.hint")}
          maxLength={120}
          autoComplete="off"
          required
          error={error?.field === "name" ? error.message : undefined}
        />
      </fieldset>
      <div className="flex flex-wrap gap-2">
        <Button type="submit" variant="primary" disabled={connect.isPending}>
          {connect.isPending ? t("settings.channels.wizard.appStep.pending") : t("settings.channels.wizard.appStep.submit")}
        </Button>
        <Button variant="ghost" disabled={connect.isPending} onClick={onBack}>
          {t("settings.channels.wizard.back")}
        </Button>
      </div>
    </form>
  );
}

/** Wizard step: the key and the secret, once. */
export function AppSecretsStep({ created, onDone }: { created: AppChannelCreated; onDone: () => void }) {
  const { t } = useTranslation();
  return (
    <div className="flex flex-col gap-4">
      <p className="m-0 text-[13px] text-ink-2">{t("settings.channels.wizard.done.app", { name: created.channel.name })}</p>
      <p role="note" className="m-0 rounded-lg border border-lamp bg-lamp-soft px-3 py-2 text-[13px] text-ink">
        {t("settings.channels.app.once")}
      </p>
      <SecretBox
        label={t("settings.channels.app.keyLabel")}
        value={created.ingest_key.secret}
        hint={t("settings.channels.app.keyHint")}
      />
      <SecretBox
        label={t("settings.channels.app.identityLabel")}
        value={created.identity_secret}
        hint={t("settings.channels.app.identityHint")}
      />
      <div>
        <Button variant="primary" onClick={onDone}>
          {t("settings.channels.app.saved")}
        </Button>
      </div>
    </div>
  );
}

/** Keys, the identity secret and the limits of an app channel, under its row in the list. */
export function AppChannelSettings({ channel, canManage }: { channel: Channel; canManage: boolean }) {
  return (
    <div className="ml-12 flex flex-col gap-5 rounded-lg border border-line bg-sunk p-3 max-nav:ml-0">
      <IngestKeys channel={channel} canManage={canManage} />
      {canManage ? <IdentitySecret channel={channel} /> : null}
      {channel.details.app_limits ? (
        <Limits channel={channel} limits={channel.details.app_limits} canManage={canManage} />
      ) : null}
    </div>
  );
}

function useShortDate() {
  const { i18n } = useTranslation();
  const fmt = new Intl.DateTimeFormat(i18n.language, { day: "numeric", month: "short", year: "numeric" });
  return (iso: string) => fmt.format(new Date(iso));
}

function IngestKeys({ channel, canManage }: { channel: Channel; canManage: boolean }) {
  const { t } = useTranslation();
  const keys = useIngestKeys(channel.id);
  const create = useCreateIngestKey();
  const [issued, setIssued] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const onCreate = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const form = event.currentTarget;
    const name = (formValues(form).name ?? "").trim();
    if (!name) return;
    setError(null);
    setIssued(null);
    create.mutate(
      { id: channel.id, name },
      {
        onSuccess: (created) => {
          form.reset();
          setIssued(created.secret);
        },
        onError: (e) => setError(describeApiError(t, e).message),
      },
    );
  };

  return (
    <section aria-labelledby={`keys-${channel.id}`} className="flex flex-col gap-2">
      <h4 id={`keys-${channel.id}`} className="m-0 text-[13px] font-semibold text-ink">
        {t("settings.channels.app.keys")}
      </h4>
      <p className="m-0 text-[12.5px] text-ink-2">{t("settings.channels.app.keysLead")}</p>
      {keys.isPending ? (
        <Skeleton className="h-10" />
      ) : keys.isError ? (
        <p role="alert" className="m-0 text-[13px] text-ink-2">
          {describeApiError(t, keys.error).message}
        </p>
      ) : (
        <ul aria-label={t("settings.channels.app.keys")} className="m-0 flex list-none flex-col divide-y divide-line p-0">
          {keys.data.map((key) => (
            <KeyRow key={key.id} channelId={channel.id} ingestKey={key} canManage={canManage} />
          ))}
        </ul>
      )}
      {canManage ? (
        <form onSubmit={onCreate} aria-busy={create.isPending} className="flex flex-wrap items-end gap-2">
          <div className="min-w-0 flex-1 basis-56">
            <TextField
              id={`new-key-${channel.id}`}
              name="name"
              label={t("settings.channels.app.newKeyLabel")}
              hint={t("settings.channels.app.newKeyHint")}
              maxLength={120}
              autoComplete="off"
              required
            />
          </div>
          <Button type="submit" variant="secondary" disabled={create.isPending} className="mb-[22px] max-nav:mb-0">
            {t("settings.channels.app.newKey")}
          </Button>
        </form>
      ) : null}
      <FormMessage tone="error" message={error} />
      {issued ? (
        <div className="flex flex-col gap-2 rounded-lg border border-lamp bg-lamp-soft p-3">
          <SecretBox label={t("settings.channels.app.keyLabel")} value={issued} hint={t("settings.channels.app.onceShort")} />
          <div>
            <Button variant="ghost" size="sm" onClick={() => setIssued(null)}>
              {t("settings.channels.app.hide")}
            </Button>
          </div>
        </div>
      ) : null}
    </section>
  );
}

function KeyRow({ channelId, ingestKey, canManage }: { channelId: string; ingestKey: IngestKey; canManage: boolean }) {
  const { t } = useTranslation();
  const date = useShortDate();
  const revoke = useRevokeIngestKey();
  const [confirm, setConfirm] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const confirmRef = useRef<HTMLButtonElement>(null);
  const revoked = ingestKey.revoked_at !== null;

  useEffect(() => {
    if (confirm) confirmRef.current?.focus();
  }, [confirm]);

  const onRevoke = () => {
    setError(null);
    revoke.mutate(
      { id: channelId, keyId: ingestKey.id },
      {
        onSuccess: () => setConfirm(false),
        onError: (e) => setError(describeApiError(t, e).message),
      },
    );
  };

  return (
    <li className="flex flex-col gap-2 py-2">
      <div className="flex flex-wrap items-start gap-x-3 gap-y-1">
        <div className="flex min-w-0 flex-1 basis-56 flex-col gap-0.5">
          <span className={cn("text-[13px] font-medium break-words", revoked ? "text-ink-3" : "text-ink")}>{ingestKey.name}</span>
          <span className="text-[12px] break-words text-ink-3">
            <code className="font-mono break-all">{ingestKey.prefix}…</code> · {t("settings.channels.app.created", { date: date(ingestKey.created_at) })}
            {" · "}
            {revoked && ingestKey.revoked_at
              ? t("settings.channels.app.revokedAt", { date: date(ingestKey.revoked_at) })
              : ingestKey.last_used_at
                ? t("settings.channels.app.lastUsed", { date: date(ingestKey.last_used_at) })
                : t("settings.channels.app.unused")}
          </span>
        </div>
        {canManage && !revoked && !confirm ? (
          <Button variant="ghost" size="sm" onClick={() => setConfirm(true)}>
            {t("settings.channels.app.revoke")}
          </Button>
        ) : null}
      </div>
      {confirm ? (
        <div role="group" aria-label={t("settings.channels.app.revoke")} className="flex flex-col gap-2 rounded-lg border border-line bg-surface p-3">
          <p className="m-0 text-[13px] text-ink">{t("settings.channels.app.revokeConfirm", { name: ingestKey.name })}</p>
          <div className="flex flex-wrap gap-2">
            <Button ref={confirmRef} variant="destructive" disabled={revoke.isPending} onClick={onRevoke}>
              {t("settings.channels.app.revoke")}
            </Button>
            <Button variant="secondary" disabled={revoke.isPending} onClick={() => setConfirm(false)}>
              {t("settings.channels.app.keepKey")}
            </Button>
          </div>
        </div>
      ) : null}
      <FormMessage tone="error" message={error} />
    </li>
  );
}

function IdentitySecret({ channel }: { channel: Channel }) {
  const { t } = useTranslation();
  const rotate = useRotateIdentitySecret();
  const [confirm, setConfirm] = useState(false);
  const [secret, setSecret] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const confirmRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    if (confirm) confirmRef.current?.focus();
  }, [confirm]);

  const onRotate = () => {
    setError(null);
    rotate.mutate(channel.id, {
      onSuccess: (next) => {
        setConfirm(false);
        setSecret(next);
      },
      onError: (e) => setError(describeApiError(t, e).message),
    });
  };

  return (
    <section aria-labelledby={`identity-${channel.id}`} className="flex flex-col gap-2">
      <h4 id={`identity-${channel.id}`} className="m-0 text-[13px] font-semibold text-ink">
        {t("settings.channels.app.identity")}
      </h4>
      <p className="m-0 text-[12.5px] text-ink-2">{t("settings.channels.app.identityLead")}</p>
      {confirm ? (
        <div role="group" aria-label={t("settings.channels.app.rotate")} className="flex flex-col gap-2 rounded-lg border border-line bg-surface p-3">
          <p className="m-0 text-[13px] text-ink">{t("settings.channels.app.rotateConfirm")}</p>
          <div className="flex flex-wrap gap-2">
            <Button ref={confirmRef} variant="destructive" disabled={rotate.isPending} onClick={onRotate}>
              {t("settings.channels.app.rotate")}
            </Button>
            <Button variant="secondary" disabled={rotate.isPending} onClick={() => setConfirm(false)}>
              {t("settings.channels.app.keepSecret")}
            </Button>
          </div>
        </div>
      ) : (
        <div>
          <Button variant="secondary" onClick={() => setConfirm(true)}>
            {t("settings.channels.app.rotate")}
          </Button>
        </div>
      )}
      <FormMessage tone="error" message={error} />
      {secret ? (
        <div className="flex flex-col gap-2 rounded-lg border border-lamp bg-lamp-soft p-3">
          <SecretBox label={t("settings.channels.app.identityLabel")} value={secret} hint={t("settings.channels.app.onceShort")} />
          <div>
            <Button variant="ghost" size="sm" onClick={() => setSecret(null)}>
              {t("settings.channels.app.hide")}
            </Button>
          </div>
        </div>
      ) : null}
    </section>
  );
}

const LIMIT_FIELDS: { name: keyof AppLimits; max: number }[] = [
  { name: "max_file_mb", max: 25 },
  { name: "max_files", max: 5 },
  { name: "max_report_mb", max: 50 },
  { name: "retention_days", max: 365 },
  { name: "daily_reports", max: 10000 },
  { name: "daily_mb", max: 100000 },
];

function Limits({ channel, limits, canManage }: { channel: Channel; limits: AppLimits; canManage: boolean }) {
  const { t, i18n } = useTranslation();
  const number = new Intl.NumberFormat(i18n.language);
  const save = useSetAppLimits();
  const [message, setMessage] = useState<{ tone: "success" | "error"; text: string } | null>(null);

  const onSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const values = formValues(event.currentTarget);
    const next = Object.fromEntries(LIMIT_FIELDS.map((f) => [f.name, Number(values[f.name])])) as AppLimits;
    setMessage(null);
    save.mutate(
      { id: channel.id, limits: next },
      {
        onSuccess: () => setMessage({ tone: "success", text: t("settings.channels.app.limitsSaved") }),
        onError: (e) => setMessage({ tone: "error", text: describeApiError(t, e).message }),
      },
    );
  };

  return (
    <section aria-labelledby={`limits-${channel.id}`} className="flex flex-col gap-2">
      <h4 id={`limits-${channel.id}`} className="m-0 text-[13px] font-semibold text-ink">
        {t("settings.channels.app.limits")}
      </h4>
      <form onSubmit={onSubmit} aria-busy={save.isPending} className="flex flex-col gap-3">
        <fieldset
          disabled={!canManage || save.isPending}
          className="m-0 grid grid-cols-[repeat(auto-fill,minmax(180px,1fr))] gap-3 border-0 p-0"
        >
          <legend className="sr-only">{t("settings.channels.app.limits")}</legend>
          {LIMIT_FIELDS.map((f) => (
            <TextField
              key={f.name}
              id={`${f.name}-${channel.id}`}
              name={f.name}
              type="number"
              inputMode="numeric"
              min={1}
              max={f.max}
              step={1}
              required
              defaultValue={limits[f.name]}
              label={t(`settings.channels.app.limit.${f.name}`)}
              hint={t("settings.channels.app.limitMax", { max: number.format(f.max) })}
              className="tabular-nums"
            />
          ))}
        </fieldset>
        {canManage ? (
          <div>
            <Button type="submit" variant="secondary" disabled={save.isPending}>
              {t("settings.channels.app.saveLimits")}
            </Button>
          </div>
        ) : null}
        <FormMessage tone="success" message={message?.tone === "success" ? message.text : null} />
        <FormMessage tone="error" message={message?.tone === "error" ? message.text : null} />
      </form>
    </section>
  );
}
