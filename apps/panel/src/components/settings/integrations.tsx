import type { GitHubIntegration, GitHubIntegrationState, GitHubTemplates } from "@tolkovo/api-client";
import {
  useCompleteGitHubApp,
  useCompleteGitHubInstallation,
  useDisconnectGitHubIntegration,
  useGitHubIntegration,
  useGitHubRepositories,
  useStartGitHubIntegration,
  useUpdateGitHubIntegration,
} from "@tolkovo/api-client/react";
import { Button, Label, Skeleton, buttonVariants, cn } from "@tolkovo/ui";
import { useNavigate } from "@tanstack/react-router";
import { CircleDot, ExternalLink } from "lucide-react";
import { useEffect, useId, useRef, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

import { describeApiError } from "../../lib/api-errors";
import { formValues } from "../../lib/forms";
import { gitHubReturn, parseLabels, postToGitHub, type IntegrationsSearch } from "../../lib/integrations/github";
import { FormMessage, SelectField, TextField } from "../form/fields";

/**
 * «Настройки → Интеграции» (GH-01, ADR-0012): the workspace's own GitHub App. The admin creates
 * it on GitHub from a manifest (issues only, no access to code), installs it on a repository and
 * picks where new issues go, their labels and the templates of the issue and of the reply about a
 * fix. GitHub sends the browser back here twice; each return finishes one step.
 */
export function IntegrationsSettings({ search }: { search: IntegrationsSearch }) {
  const { t } = useTranslation();
  const state = useGitHubIntegration();
  const [notice, setNotice] = useState<string | null>(null);
  const returnError = useGitHubReturn(search, setNotice);

  return (
    <section aria-labelledby="settings-integrations-title" aria-busy={state.isPending} className="flex flex-col gap-4">
      <h2 id="settings-integrations-title" className="m-0 text-base font-semibold">
        {t("settings.integrations.title")}
      </h2>
      <p className="m-0 max-w-[640px] text-[13px] text-ink-2">{t("settings.integrations.lead")}</p>
      <FormMessage tone="success" message={notice} />
      <FormMessage tone="error" message={returnError} />
      {state.isPending ? (
        <div role="status" className="max-w-[720px]">
          <span className="sr-only">{t("app.loading")}</span>
          <Skeleton className="h-40" />
        </div>
      ) : state.isError ? (
        <div role="alert" className="flex flex-col items-start gap-2">
          <p className="m-0 text-ink-2">{describeApiError(t, state.error).message}</p>
          <Button variant="secondary" onClick={() => void state.refetch()}>
            {t("error.retry")}
          </Button>
        </div>
      ) : (
        <GitHubCard state={state.data} onNotice={setNotice} />
      )}
    </section>
  );
}

/**
 * Finishes the step GitHub returned from, once, then takes code and state out of the address so a
 * reload does not send them again.
 */
function useGitHubReturn(search: IntegrationsSearch, onNotice: (m: string | null) => void): string | null {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const completeApp = useCompleteGitHubApp();
  const completeInstall = useCompleteGitHubInstallation();
  const [error, setError] = useState<string | null>(null);
  const done = useRef(false);
  const ret = gitHubReturn(search);
  const key = ret ? JSON.stringify(ret) : null;

  useEffect(() => {
    if (!key || done.current) return;
    done.current = true;
    const step = JSON.parse(key) as NonNullable<typeof ret>;
    const clear = () => void navigate({ to: "/settings/integrations", search: {}, replace: true });
    const onError = (e: unknown) => {
      setError(describeApiError(t, e).message);
      clear();
    };
    if (step.github === "created") {
      completeApp.mutate(
        { code: step.code, state: step.state },
        {
          onSuccess: () => {
            onNotice(t("settings.integrations.github.created"));
            clear();
          },
          onError,
        },
      );
    } else {
      completeInstall.mutate(step.installation_id, {
        onSuccess: () => {
          onNotice(t("settings.integrations.github.installed"));
          clear();
        },
        onError,
      });
    }
  }, [key, completeApp, completeInstall, navigate, onNotice, t]);

  return error;
}

function GitHubCard({ state, onNotice }: { state: GitHubIntegrationState; onNotice: (m: string | null) => void }) {
  const { t } = useTranslation();
  const g = state.integration;
  return (
    <article
      aria-labelledby="github-title"
      className="flex max-w-[720px] flex-col gap-4 rounded-xl border border-line bg-surface p-4"
    >
      <div className="flex items-start gap-3">
        <span aria-hidden="true" className="grid size-9 shrink-0 place-items-center rounded-lg bg-teal-soft text-teal">
          <CircleDot className="size-[18px]" strokeWidth={1.75} />
        </span>
        <div className="flex min-w-0 flex-1 flex-col gap-0.5">
          <h3 id="github-title" className="m-0 text-[14px] font-semibold text-ink">
            {t("settings.integrations.github.title")}
          </h3>
          <p className="m-0 text-[12.5px] text-ink-3">{t(`settings.integrations.github.status.${g?.status ?? "none"}`)}</p>
        </div>
      </div>
      {!state.encryption_available ? (
        <p role="note" className="m-0 rounded-lg border border-note-line bg-note px-3 py-2 text-[13px] text-ink">
          {t("settings.integrations.github.noKey")}
        </p>
      ) : !g ? (
        <StartStep onNotice={onNotice} />
      ) : g.status === "app_pending" ? (
        <WaitingStep onNotice={onNotice} />
      ) : g.status === "install_pending" ? (
        <InstallStep integration={g} onNotice={onNotice} />
      ) : (
        <>
          <ConnectedFacts integration={g} />
          <SettingsForm key={g.updated_at} integration={g} state={state} onNotice={onNotice} />
          <Disconnect integration={g} onNotice={onNotice} />
        </>
      )}
    </article>
  );
}

/** Before anything: what goes to GitHub, then the App is created there from the manifest. */
function StartStep({ onNotice }: { onNotice: (m: string | null) => void }) {
  const { t } = useTranslation();
  const start = useStartGitHubIntegration();
  const [error, setError] = useState<{ field?: "organization"; message: string } | null>(null);

  const onSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const organization = (formValues(event.currentTarget).organization ?? "").trim();
    setError(null);
    onNotice(null);
    start.mutate(organization, {
      onSuccess: (started) => postToGitHub(started.post_url, started.manifest),
      onError: (e) => setError(describeApiError<"organization">(t, e, { invalid_request: "organization" })),
    });
  };

  return (
    <form onSubmit={onSubmit} noValidate aria-busy={start.isPending} className="flex flex-col gap-3">
      <DataNote />
      <p className="m-0 text-[13px] text-ink-2">{t("settings.integrations.github.startText")}</p>
      <FormMessage tone="error" message={error && !error.field ? error.message : null} />
      <fieldset disabled={start.isPending} className="m-0 flex max-w-[360px] flex-col gap-3 border-0 p-0">
        <TextField
          id="github-organization"
          name="organization"
          label={t("settings.integrations.github.organization")}
          hint={t("settings.integrations.github.organizationHint")}
          maxLength={39}
          autoComplete="off"
          spellCheck={false}
          error={error?.field === "organization" ? error.message : undefined}
        />
      </fieldset>
      <div>
        <Button type="submit" variant="primary" disabled={start.isPending}>
          {start.isPending ? t("settings.integrations.github.starting") : t("settings.integrations.github.start")}
        </Button>
      </div>
    </form>
  );
}

function DataNote() {
  const { t } = useTranslation();
  return (
    <p role="note" className="m-0 rounded-lg border border-note-line bg-note px-3 py-2 text-[13px] text-ink">
      {t("settings.integrations.github.dataNote")}
    </p>
  );
}

/**
 * The manifest went to GitHub, but GitHub has not sent the browser back yet. Starting again shows
 * the first step, so the organization can be changed too.
 */
function WaitingStep({ onNotice }: { onNotice: (m: string | null) => void }) {
  const { t } = useTranslation();
  const cancel = useDisconnectGitHubIntegration();
  const [restart, setRestart] = useState(false);
  const [error, setError] = useState<string | null>(null);
  if (restart) return <StartStep onNotice={onNotice} />;
  return (
    <div className="flex flex-col gap-3">
      <p className="m-0 text-[13px] text-ink-2">{t("settings.integrations.github.waiting")}</p>
      <FormMessage tone="error" message={error} />
      <div className="flex flex-wrap gap-2">
        <Button variant="primary" disabled={cancel.isPending} onClick={() => setRestart(true)}>
          {t("settings.integrations.github.restart")}
        </Button>
        <Button
          variant="ghost"
          disabled={cancel.isPending}
          onClick={() => {
            onNotice(null);
            cancel.mutate(undefined, { onError: (e) => setError(describeApiError(t, e).message) });
          }}
        >
          {t("settings.integrations.github.cancel")}
        </Button>
      </div>
    </div>
  );
}

/** The App exists on GitHub; it waits to be installed on the repositories. */
function InstallStep({ integration, onNotice }: { integration: GitHubIntegration; onNotice: (m: string | null) => void }) {
  const { t } = useTranslation();
  const cancel = useDisconnectGitHubIntegration();
  const [error, setError] = useState<string | null>(null);
  const app = integration.app;
  return (
    <div className="flex flex-col gap-3">
      <p className="m-0 text-[13px] text-ink-2">{t("settings.integrations.github.installText", { slug: app?.slug ?? "" })}</p>
      <FormMessage tone="error" message={error} />
      <div className="flex flex-wrap gap-2">
        {integration.install_url ? (
          <a href={integration.install_url} className={buttonVariants({ variant: "primary" })}>
            {t("settings.integrations.github.install")}
            <ExternalLink aria-hidden="true" />
          </a>
        ) : null}
        <Button
          variant="ghost"
          disabled={cancel.isPending}
          onClick={() => {
            onNotice(null);
            cancel.mutate(undefined, { onError: (e) => setError(describeApiError(t, e).message) });
          }}
        >
          {t("settings.integrations.github.cancel")}
        </Button>
      </div>
      {app ? <p className="m-0 text-[12.5px] text-ink-3">{t("settings.integrations.github.cancelNote")}</p> : null}
    </div>
  );
}

function ConnectedFacts({ integration }: { integration: GitHubIntegration }) {
  const { t } = useTranslation();
  const app = integration.app;
  if (!app) return null;
  return (
    <p className="m-0 flex flex-wrap items-center gap-x-2 gap-y-1 text-[13px] text-ink-2">
      <span>{t("settings.integrations.github.connectedAs", { slug: app.slug, account: integration.account ?? app.owner })}</span>
      <a href={app.url} target="_blank" rel="noreferrer" className="inline-flex items-center gap-1 text-teal-ink underline">
        {t("settings.integrations.github.openApp")}
        <ExternalLink aria-hidden="true" className="size-3.5" />
      </a>
    </p>
  );
}

type TemplateField = keyof GitHubTemplates;
const issueFields: TemplateField[] = ["issue_title", "issue_body"];
const replyFields: TemplateField[] = ["reply_ru", "reply_en"];

/** Where issues go, their labels and the templates; one save for all. */
function SettingsForm({
  integration,
  state,
  onNotice,
}: {
  integration: GitHubIntegration;
  state: GitHubIntegrationState;
  onNotice: (m: string | null) => void;
}) {
  const { t } = useTranslation();
  const update = useUpdateGitHubIntegration();
  const repos = useGitHubRepositories();
  const [templates, setTemplates] = useState<GitHubTemplates>(integration.templates);
  const [repo, setRepo] = useState(integration.repository?.full_name ?? "");
  const [error, setError] = useState<{ field?: "repository" | "labels"; message: string } | null>(null);
  const listed = repos.data && !repos.data.more ? repos.data.items : null;
  const chosen = listed?.find((r) => r.full_name === repo) ?? (integration.repository?.full_name === repo ? integration.repository : null);

  const onSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const values = formValues(event.currentTarget);
    setError(null);
    onNotice(null);
    update.mutate(
      { repository: repo.trim(), labels: parseLabels(values.labels ?? ""), templates },
      {
        onSuccess: () => onNotice(t("settings.integrations.github.saved")),
        onError: (e) =>
          setError(
            describeApiError<"repository" | "labels">(t, e, { github_repository_unreachable: "repository" }),
          ),
      },
    );
  };

  return (
    <form onSubmit={onSubmit} noValidate aria-busy={update.isPending} className="flex flex-col gap-4 border-t border-line pt-4">
      <FormMessage tone="error" message={error && !error.field ? error.message : null} />
      <fieldset disabled={update.isPending} className="m-0 flex flex-col gap-4 border-0 p-0">
        <legend className="mb-2 p-0 text-[13px] font-semibold text-ink">{t("settings.integrations.github.whereTitle")}</legend>
        <div className="max-w-[420px]">
          {listed ? (
            <SelectField
              id="github-repository"
              name="repository"
              label={t("settings.integrations.github.repository")}
              value={repo}
              onChange={(e) => setRepo(e.currentTarget.value)}
              error={error?.field === "repository" ? error.message : undefined}
            >
              <option value="">{t("settings.integrations.github.noRepository")}</option>
              {listed.map((r) => (
                <option key={r.id} value={r.full_name}>
                  {r.full_name}
                </option>
              ))}
            </SelectField>
          ) : (
            <TextField
              id="github-repository"
              name="repository"
              label={t("settings.integrations.github.repository")}
              hint={
                repos.isError
                  ? describeApiError(t, repos.error).message
                  : repos.isPending
                    ? t("app.loading")
                    : t("settings.integrations.github.repositoryHint")
              }
              value={repo}
              onChange={(e) => setRepo(e.currentTarget.value)}
              maxLength={200}
              autoComplete="off"
              spellCheck={false}
              error={error?.field === "repository" ? error.message : undefined}
            />
          )}
        </div>
        {chosen && !chosen.private ? (
          <p role="note" className="m-0 max-w-[640px] rounded-lg border border-lamp bg-lamp-soft px-3 py-2 text-[13px] text-ink">
            {t("settings.integrations.github.publicRepo")}
          </p>
        ) : null}
        <div className="max-w-[420px]">
          <TextField
            id="github-labels"
            name="labels"
            label={t("settings.integrations.github.labels")}
            hint={t("settings.integrations.github.labelsHint")}
            defaultValue={integration.labels.join(", ")}
            autoComplete="off"
          />
        </div>
      </fieldset>
      <fieldset disabled={update.isPending} className="m-0 flex flex-col gap-4 border-0 p-0">
        <legend className="mb-2 p-0 text-[13px] font-semibold text-ink">{t("settings.integrations.github.templatesTitle")}</legend>
        <Placeholders names={state.placeholders.issue} />
        {issueFields.map((f) => (
          <TemplateInput key={f} field={f} value={templates[f]} def={state.defaults[f]} onChange={(v) => setTemplates({ ...templates, [f]: v })} />
        ))}
        <Placeholders names={state.placeholders.reply} />
        {replyFields.map((f) => (
          <TemplateInput key={f} field={f} value={templates[f]} def={state.defaults[f]} onChange={(v) => setTemplates({ ...templates, [f]: v })} />
        ))}
      </fieldset>
      <div>
        <Button type="submit" variant="primary" disabled={update.isPending}>
          {t("settings.integrations.github.save")}
        </Button>
      </div>
    </form>
  );
}

/** «{summary} — суть проблемы, …»: placeholders come from the server, their meaning from here. */
function Placeholders({ names }: { names: string[] }) {
  const { t } = useTranslation();
  return (
    <p className="m-0 text-[12.5px] text-ink-3">
      {t("settings.integrations.github.placeholders")}{" "}
      {names.map((n, i) => (
        <span key={n}>
          <code className="rounded bg-sunk px-1 font-mono text-[12px] text-ink-2">{`{${n}}`}</code>
          {knownPlaceholder(n) ? ` — ${t(`settings.integrations.github.placeholder.${n}`)}` : ""}
          {i < names.length - 1 ? ", " : ""}
        </span>
      ))}
    </p>
  );
}

const placeholderNames = ["summary", "app_name", "app_version", "os", "os_version", "conversation_url", "name", "issue_number"] as const;
type PlaceholderName = (typeof placeholderNames)[number];
function knownPlaceholder(n: string): n is PlaceholderName {
  return (placeholderNames as readonly string[]).includes(n);
}

function TemplateInput({
  field,
  value,
  def,
  onChange,
}: {
  field: TemplateField;
  value: string;
  def: string;
  onChange: (v: string) => void;
}) {
  const { t } = useTranslation();
  const id = useId();
  const control = cn(
    "w-full min-w-0 rounded-lg border border-input bg-surface px-2.5 text-sm text-ink",
    "focus-visible:border-ring focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-ring",
    "disabled:cursor-not-allowed disabled:opacity-55",
  );
  return (
    <div className="flex flex-col gap-1.5">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <Label htmlFor={id}>{t(`settings.integrations.github.template.${field}`)}</Label>
        {value !== def ? (
          <Button variant="ghost" size="sm" onClick={() => onChange(def)}>
            {t("settings.integrations.github.reset")}
          </Button>
        ) : null}
      </div>
      {field === "issue_title" ? (
        <input id={id} value={value} maxLength={256} onChange={(e) => onChange(e.currentTarget.value)} className={cn(control, "h-9")} />
      ) : (
        <textarea
          id={id}
          value={value}
          rows={field === "issue_body" ? 10 : 3}
          maxLength={field === "issue_body" ? 20000 : 4000}
          onChange={(e) => onChange(e.currentTarget.value)}
          className={cn(control, "py-2 font-mono text-[12.5px] leading-relaxed")}
        />
      )}
    </div>
  );
}

function Disconnect({ integration, onNotice }: { integration: GitHubIntegration; onNotice: (m: string | null) => void }) {
  const { t } = useTranslation();
  const disconnect = useDisconnectGitHubIntegration();
  const [confirm, setConfirm] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const confirmRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    if (confirm) confirmRef.current?.focus();
  }, [confirm]);

  return (
    <div className="flex flex-col gap-2 border-t border-line pt-4">
      {confirm ? (
        <div role="group" aria-label={t("settings.integrations.github.disconnect")} className="flex flex-col gap-2 rounded-lg border border-line bg-sunk p-3">
          <p className="m-0 text-[13px] text-ink">
            {t("settings.integrations.github.disconnectConfirm", { slug: integration.app?.slug ?? "" })}
          </p>
          <div className="flex flex-wrap gap-2">
            <Button
              ref={confirmRef}
              variant="destructive"
              disabled={disconnect.isPending}
              onClick={() => {
                setError(null);
                disconnect.mutate(undefined, {
                  onSuccess: () => onNotice(t("settings.integrations.github.disconnected")),
                  onError: (e) => {
                    setConfirm(false);
                    setError(describeApiError(t, e).message);
                  },
                });
              }}
            >
              {t("settings.integrations.github.disconnect")}
            </Button>
            <Button variant="secondary" disabled={disconnect.isPending} onClick={() => setConfirm(false)}>
              {t("settings.integrations.github.keep")}
            </Button>
          </div>
        </div>
      ) : (
        <div>
          <Button variant="ghost" onClick={() => setConfirm(true)}>
            {t("settings.integrations.github.disconnect")}
          </Button>
        </div>
      )}
      <FormMessage tone="error" message={error} />
    </div>
  );
}
