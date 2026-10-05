import type { Channel, WidgetChannelCreated } from "@tolkovo/api-client";
import { useConnectWidgetChannel, useSetWidgetOrigins } from "@tolkovo/api-client/react";
import { Button, Label, cn } from "@tolkovo/ui";
import { useId, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

import { describeApiError } from "../../lib/api-errors";
import { formValues } from "../../lib/forms";
import { FormMessage, TextField } from "../form/fields";
import { IdentitySecret, SecretBox } from "./app-channel";

/**
 * The «Виджет» channel (docs/04 §6, W-01): a chat on a site or inside the company's app. The
 * widget key is public — it goes into the page — and the server answers only the pages on the
 * list. The identity secret stays on the company's backend, which signs who the visitor is
 * (W-04); it is shown once. How the widget looks is set in code, not here (docs/README §1).
 */

/** One origin per line or separated by commas; empty lines dropped. */
function parseOrigins(text: string): string[] {
  return text
    .split(/[\n,]/)
    .map((o) => o.trim())
    .filter(Boolean);
}

const textarea = cn(
  "w-full min-w-0 rounded-lg border border-input bg-surface px-2.5 py-2 font-mono text-[12.5px] leading-relaxed text-ink",
  "focus-visible:border-ring focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-ring",
  "disabled:cursor-not-allowed disabled:opacity-55",
);

function OriginsField({ id, defaultValue, error }: { id: string; defaultValue?: string; error?: string | undefined }) {
  const { t } = useTranslation();
  const hintId = `${id}-hint`;
  const errorId = `${id}-error`;
  return (
    <div className="flex flex-col gap-1.5">
      <Label htmlFor={id}>{t("settings.channels.widget.originsLabel")}</Label>
      <textarea
        id={id}
        name="origins"
        rows={4}
        defaultValue={defaultValue}
        spellCheck={false}
        autoComplete="off"
        aria-invalid={error ? true : undefined}
        aria-describedby={error ? `${errorId} ${hintId}` : hintId}
        className={textarea}
      />
      {error ? (
        <p id={errorId} className="m-0 text-[12.5px] text-danger">
          {error}
        </p>
      ) : null}
      <p id={hintId} className="m-0 text-[12.5px] text-ink-3">
        {t("settings.channels.widget.originsHint")}
      </p>
    </div>
  );
}

/** Wizard step: the name and the pages, then the server issues the identity secret. */
export function WidgetStep({ onBack, onDone }: { onBack: () => void; onDone: (created: WidgetChannelCreated) => void }) {
  const { t } = useTranslation();
  const connect = useConnectWidgetChannel();
  const [error, setError] = useState<{ field?: "origins"; message: string } | null>(null);

  const onSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const values = formValues(event.currentTarget);
    setError(null);
    connect.mutate(
      { name: (values.name ?? "").trim(), allowed_origins: parseOrigins(values.origins ?? "") },
      { onSuccess: onDone, onError: (e) => setError(describeApiError<"origins">(t, e, { invalid_request: "origins" })) },
    );
  };

  return (
    <form onSubmit={onSubmit} noValidate aria-busy={connect.isPending} className="flex flex-col gap-3">
      <p className="m-0 max-w-[600px] text-[13px] text-ink-2">{t("settings.channels.wizard.widgetStep.text")}</p>
      <FormMessage tone="error" message={error && !error.field ? error.message : null} />
      <fieldset disabled={connect.isPending} className="m-0 flex max-w-[480px] flex-col gap-3 border-0 p-0">
        <TextField
          id="widget-name"
          name="name"
          label={t("settings.channels.wizard.widgetStep.label")}
          hint={t("settings.channels.wizard.widgetStep.hint")}
          maxLength={120}
          autoComplete="off"
          required
        />
        <OriginsField id="widget-origins" error={error?.field === "origins" ? error.message : undefined} />
      </fieldset>
      <div className="flex flex-wrap gap-2">
        <Button type="submit" variant="primary" disabled={connect.isPending}>
          {connect.isPending ? t("settings.channels.wizard.widgetStep.pending") : t("settings.channels.wizard.widgetStep.submit")}
        </Button>
        <Button variant="ghost" disabled={connect.isPending} onClick={onBack}>
          {t("settings.channels.wizard.back")}
        </Button>
      </div>
    </form>
  );
}

/** The code the developer pastes into the app: the key and this server. */
function installSnippet(key: string): string {
  return [
    'import { Tolkovo } from "@tolkovo/widget";',
    "",
    "Tolkovo.init({",
    `  widgetKey: "${key}",`,
    `  apiBase: "${window.location.origin}",`,
    "});",
  ].join("\n");
}

function Snippet({ widgetKey }: { widgetKey: string }) {
  const { t } = useTranslation();
  const id = useId();
  const [copied, setCopied] = useState(false);
  const code = installSnippet(widgetKey);
  return (
    <div className="flex flex-col gap-1.5">
      <span id={id} className="text-[13px] font-medium text-ink">
        {t("settings.channels.widget.snippet")}
      </span>
      <pre
        aria-labelledby={id}
        tabIndex={0}
        className="m-0 overflow-x-auto rounded-lg border border-line bg-sunk p-3 font-mono text-[12.5px] leading-relaxed text-ink"
      >
        {code}
      </pre>
      <div className="flex flex-wrap items-center gap-2">
        <Button
          variant="secondary"
          size="sm"
          onClick={() =>
            void navigator.clipboard?.writeText(code).then(
              () => setCopied(true),
              () => setCopied(false),
            )
          }
        >
          {t("settings.channels.app.copy")}
        </Button>
        <span className="text-[12.5px] text-ink-3">{t("settings.channels.widget.snippetHint")}</span>
      </div>
      <FormMessage tone="success" message={copied ? t("settings.channels.app.copied") : null} />
    </div>
  );
}

/** Wizard step: the identity secret, once; the key and the snippet are always in the settings. */
export function WidgetSecretsStep({ created, onDone }: { created: WidgetChannelCreated; onDone: () => void }) {
  const { t } = useTranslation();
  const key = created.channel.details.widget_key ?? "";
  return (
    <div className="flex flex-col gap-4">
      <p className="m-0 text-[13px] text-ink-2">{t("settings.channels.wizard.done.widget", { name: created.channel.name })}</p>
      {(created.channel.details.allowed_origins ?? []).length === 0 ? (
        <p role="note" className="m-0 rounded-lg border border-line bg-sunk px-3 py-2 text-[13px] text-ink">
          {t("settings.channels.widget.noOrigins")}
        </p>
      ) : null}
      <SecretBox label={t("settings.channels.widget.keyLabel")} value={key} hint={t("settings.channels.widget.keyHint")} />
      <Snippet widgetKey={key} />
      <p role="note" className="m-0 rounded-lg border border-lamp bg-lamp-soft px-3 py-2 text-[13px] text-ink">
        {t("settings.channels.widget.once")}
      </p>
      <SecretBox
        label={t("settings.channels.app.identityLabel")}
        value={created.identity_secret}
        hint={t("settings.channels.widget.identityHint")}
      />
      <div>
        <Button variant="primary" onClick={onDone}>
          {t("settings.channels.app.saved")}
        </Button>
      </div>
    </div>
  );
}

/** The key, the pages and the identity secret of a widget, under its row in the list. */
export function WidgetChannelSettings({ channel, canManage }: { channel: Channel; canManage: boolean }) {
  const { t } = useTranslation();
  const key = channel.details.widget_key ?? "";
  return (
    <div className="ml-12 flex flex-col gap-5 rounded-lg border border-line bg-sunk p-3 max-nav:ml-0">
      <section aria-labelledby={`widget-key-${channel.id}`} className="flex flex-col gap-3">
        <h4 id={`widget-key-${channel.id}`} className="m-0 text-[13px] font-semibold text-ink">
          {t("settings.channels.widget.install")}
        </h4>
        <SecretBox label={t("settings.channels.widget.keyLabel")} value={key} hint={t("settings.channels.widget.keyHint")} />
        <Snippet widgetKey={key} />
      </section>
      <Origins channel={channel} canManage={canManage} />
      {canManage ? <IdentitySecret channel={channel} /> : null}
    </div>
  );
}

function Origins({ channel, canManage }: { channel: Channel; canManage: boolean }) {
  const { t } = useTranslation();
  const save = useSetWidgetOrigins();
  const [message, setMessage] = useState<{ tone: "success" | "error"; text: string } | null>(null);
  const origins = channel.details.allowed_origins ?? [];

  const onSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setMessage(null);
    save.mutate(
      { id: channel.id, origins: parseOrigins(formValues(event.currentTarget).origins ?? "") },
      {
        onSuccess: () => setMessage({ tone: "success", text: t("settings.channels.widget.originsSaved") }),
        onError: (e) => setMessage({ tone: "error", text: describeApiError(t, e).message }),
      },
    );
  };

  return (
    <section aria-labelledby={`widget-origins-${channel.id}`} className="flex flex-col gap-2">
      <h4 id={`widget-origins-${channel.id}`} className="m-0 text-[13px] font-semibold text-ink">
        {t("settings.channels.widget.origins")}
      </h4>
      {canManage ? (
        <form onSubmit={onSubmit} aria-busy={save.isPending} className="flex max-w-[480px] flex-col gap-3">
          <fieldset disabled={save.isPending} className="m-0 border-0 p-0">
            <OriginsField id={`origins-${channel.id}`} defaultValue={origins.join("\n")} />
          </fieldset>
          <div>
            <Button type="submit" variant="secondary" disabled={save.isPending}>
              {t("settings.channels.widget.saveOrigins")}
            </Button>
          </div>
        </form>
      ) : origins.length > 0 ? (
        <ul className="m-0 flex list-none flex-col gap-1 p-0">
          {origins.map((o) => (
            <li key={o}>
              <code className="font-mono text-[12.5px] break-all text-ink">{o}</code>
            </li>
          ))}
        </ul>
      ) : (
        <p className="m-0 text-[12.5px] text-ink-3">{t("settings.channels.widget.noOrigins")}</p>
      )}
      <FormMessage tone="success" message={message?.tone === "success" ? message.text : null} />
      <FormMessage tone="error" message={message?.tone === "error" ? message.text : null} />
    </section>
  );
}
