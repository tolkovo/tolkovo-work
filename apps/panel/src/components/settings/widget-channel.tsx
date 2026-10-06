import type { Channel, WidgetAnalytics, WidgetChannelCreated } from "@tolkovo/api-client";
import { useConnectWidgetChannel, useSetWidgetAnalytics, useSetWidgetOrigins } from "@tolkovo/api-client/react";
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
 * (W-04); it is shown once. How the widget looks is set in code, not here (docs/README §1); which
 * site counters get its goals is set here (W-05).
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
      <Analytics channel={channel} canManage={canManage} />
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

const GOALS = [
  { key: "chat_started", label: "settings.channels.widget.goalChatStarted" },
  { key: "contact_collected", label: "settings.channels.widget.goalContactCollected" },
  { key: "rated", label: "settings.channels.widget.goalRated" },
] as const;
const GOAL_PATTERN = /^[A-Za-z0-9_.:-]{1,64}$/;
const DEFAULT_GOALS = { chat_started: "tolkovo.chatStarted", contact_collected: "tolkovo.contactCollected", rated: "tolkovo.rated" };

type AnalyticsField = "metrika" | (typeof GOALS)[number]["key"];

/** Which of the site's counters get the widget's goals (docs/04 §6.6). */
function Analytics({ channel, canManage }: { channel: Channel; canManage: boolean }) {
  const { t } = useTranslation();
  const save = useSetWidgetAnalytics();
  const [message, setMessage] = useState<{ tone: "success" | "error"; text: string } | null>(null);
  const [errors, setErrors] = useState<Partial<Record<AnalyticsField, string>>>({});
  const a: WidgetAnalytics = channel.details.analytics ?? { data_layer: false, gtag: false, metrika_counter: null, goals: DEFAULT_GOALS };
  const id = (name: string) => `widget-${name}-${channel.id}`;

  const onSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    const text = (name: string) => {
      const v = form.get(name);
      return typeof v === "string" ? v.trim() : "";
    };
    const next: WidgetAnalytics = {
      data_layer: form.get("data_layer") === "on",
      gtag: form.get("gtag") === "on",
      metrika_counter: text("metrika") || null,
      goals: { chat_started: text("chat_started"), contact_collected: text("contact_collected"), rated: text("rated") },
    };
    const found: Partial<Record<AnalyticsField, string>> = {};
    if (next.metrika_counter && !/^[0-9]{1,12}$/.test(next.metrika_counter)) found.metrika = t("settings.channels.widget.metrikaHint");
    for (const g of GOALS) if (next.goals[g.key] && !GOAL_PATTERN.test(next.goals[g.key])) found[g.key] = t("settings.channels.widget.goalsHint");
    setErrors(found);
    setMessage(null);
    const first = (["metrika", ...GOALS.map((g) => g.key)] as const).find((f) => found[f]);
    if (first) {
      document.getElementById(id(first))?.focus();
      return;
    }
    save.mutate(
      { id: channel.id, analytics: next },
      {
        onSuccess: () => setMessage({ tone: "success", text: t("settings.channels.widget.analyticsSaved") }),
        onError: (e) => setMessage({ tone: "error", text: describeApiError(t, e).message }),
      },
    );
  };

  const targets = [a.data_layer ? "dataLayer" : "", a.gtag ? "gtag" : "", a.metrika_counter ? t("settings.channels.widget.metrikaTarget", { counter: a.metrika_counter }) : ""].filter(Boolean);
  return (
    <section aria-labelledby={id("analytics")} className="flex flex-col gap-2">
      <h4 id={id("analytics")} className="m-0 text-[13px] font-semibold text-ink">
        {t("settings.channels.widget.analytics")}
      </h4>
      <p className="m-0 max-w-[600px] text-[12.5px] text-ink-3">{t("settings.channels.widget.analyticsLead")}</p>
      {canManage ? (
        <form onSubmit={onSubmit} noValidate aria-busy={save.isPending} className="flex max-w-[480px] flex-col gap-3">
          <fieldset disabled={save.isPending} className="m-0 flex flex-col gap-3 border-0 p-0">
            {(["data_layer", "gtag"] as const).map((name) => (
              <div key={name} className="flex items-start gap-2.5">
                <input
                  id={id(name)}
                  name={name}
                  type="checkbox"
                  defaultChecked={a[name]}
                  aria-describedby={`${id(name)}-hint`}
                  className="mt-0.5 size-4 shrink-0 accent-teal"
                />
                <div className="flex min-w-0 flex-1 flex-col gap-0.5">
                  <label htmlFor={id(name)} className="text-[13px] text-ink">
                    {name === "data_layer" ? t("settings.channels.widget.dataLayer") : t("settings.channels.widget.gtag")}
                  </label>
                  <p id={`${id(name)}-hint`} className="m-0 font-mono text-[12px] break-words text-ink-3">
                    {name === "data_layer" ? t("settings.channels.widget.dataLayerHint") : t("settings.channels.widget.gtagHint")}
                  </p>
                </div>
              </div>
            ))}
            <TextField
              id={id("metrika")}
              name="metrika"
              label={t("settings.channels.widget.metrika")}
              hint={t("settings.channels.widget.metrikaHint")}
              error={errors.metrika}
              defaultValue={a.metrika_counter ?? ""}
              inputMode="numeric"
              maxLength={12}
              autoComplete="off"
            />
            <details className="group flex flex-col gap-3">
              <summary className="cursor-pointer text-[13px] font-medium text-ink">{t("settings.channels.widget.goals")}</summary>
              <div className="mt-3 flex flex-col gap-3">
                {GOALS.map((g) => (
                  <TextField
                    key={g.key}
                    id={id(g.key)}
                    name={g.key}
                    label={t(g.label)}
                    error={errors[g.key]}
                    defaultValue={a.goals[g.key]}
                    placeholder={DEFAULT_GOALS[g.key]}
                    maxLength={64}
                    autoComplete="off"
                    spellCheck={false}
                    className="font-mono"
                  />
                ))}
                <p className="m-0 text-[12.5px] text-ink-3">{t("settings.channels.widget.goalsHint")}</p>
              </div>
            </details>
          </fieldset>
          <div>
            <Button type="submit" variant="secondary" disabled={save.isPending}>
              {t("settings.channels.widget.saveAnalytics")}
            </Button>
          </div>
        </form>
      ) : (
        <p className="m-0 text-[12.5px] text-ink-2">{targets.length > 0 ? t("settings.channels.widget.analyticsOn", { targets: targets.join(", ") }) : t("settings.channels.widget.analyticsOff")}</p>
      )}
      <FormMessage tone="success" message={message?.tone === "success" ? message.text : null} />
      <FormMessage tone="error" message={message?.tone === "error" ? message.text : null} />
    </section>
  );
}
