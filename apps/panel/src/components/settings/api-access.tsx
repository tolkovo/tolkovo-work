import { isApiError, type AccessToken, type AccessTokenScope } from "@tolkovo/api-client";
import {
  useAccessTokens,
  useCan,
  useCreateAccessToken,
  useMyAccessTokens,
  useRevokeAccessToken,
  useRevokeMyAccessToken,
} from "@tolkovo/api-client/react";
import { Button, Skeleton } from "@tolkovo/ui";
import { KeyRound } from "lucide-react";
import { useId, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

import { describeApiError } from "../../lib/api-errors";
import { formValues } from "../../lib/forms";
import { FormMessage, SelectField, TextField } from "../form/fields";
import { SecretBox } from "./app-channel";

/**
 * Personal access tokens (ADR-0016): a member issues a token for a script or an AI agent that
 * works with tasks on their behalf, and connects Claude Code to the MCP server with it. Admins
 * also see every member's tokens and revoke any.
 */
export function ApiAccess() {
  const { t } = useTranslation();
  const canManage = useCan("members.manage");
  return (
    <div className="flex flex-col gap-8">
      <p className="m-0 max-w-[640px] text-[13px] text-ink-2">{t("settings.api.lead")}</p>
      <NewToken />
      <MyTokens />
      {canManage ? <AllTokens /> : null}
    </div>
  );
}

const EXPIRY = ["30", "90", "365", "never"] as const;

/** The MCP endpoint of this server, as the agent will reach it. */
function mcpURL(): string {
  return `${window.location.origin}/api/v1/mcp`;
}

function NewToken() {
  const { t } = useTranslation();
  const id = useId();
  const create = useCreateAccessToken();
  const [issued, setIssued] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const onSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const form = event.currentTarget;
    const v = formValues(form);
    const name = (v.name ?? "").trim();
    if (!name) return;
    const scopes: AccessTokenScope[] = v.access === "write" ? ["tasks:write"] : ["tasks:read"];
    const expires = v.expires === "never" ? null : Number(v.expires ?? "90");
    setError(null);
    setIssued(null);
    create.mutate(
      { name, scopes, expires_in_days: expires },
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
    <section aria-labelledby={`${id}-title`} className="flex max-w-[640px] flex-col gap-3">
      <h3 id={`${id}-title`} className="m-0 text-sm font-semibold">
        {t("settings.api.new.title")}
      </h3>
      <form onSubmit={onSubmit} aria-busy={create.isPending} className="flex flex-col gap-3">
        <TextField
          id={`${id}-name`}
          name="name"
          label={t("settings.api.new.name")}
          hint={t("settings.api.new.nameHint")}
          maxLength={80}
          autoComplete="off"
          required
        />
        <div className="grid grid-cols-2 gap-3 max-nav:grid-cols-1">
          <SelectField id={`${id}-access`} name="access" label={t("settings.api.new.access")} defaultValue="write">
            <option value="write">{t("settings.api.new.write")}</option>
            <option value="read">{t("settings.api.new.read")}</option>
          </SelectField>
          <SelectField id={`${id}-expires`} name="expires" label={t("settings.api.new.expires")} defaultValue="90">
            {EXPIRY.map((d) => (
              <option key={d} value={d}>
                {t(d === "never" ? "settings.api.new.never" : `settings.api.new.days${d}`)}
              </option>
            ))}
          </SelectField>
        </div>
        <div>
          <Button type="submit" disabled={create.isPending}>
            {create.isPending ? t("settings.api.new.pending") : t("settings.api.new.submit")}
          </Button>
        </div>
      </form>
      <FormMessage tone="error" message={error} />
      {issued ? (
        <div className="flex flex-col gap-4 rounded-lg border border-lamp bg-lamp-soft p-3">
          <p className="m-0 text-[13px] font-semibold text-ink">{t("settings.api.issued.title")}</p>
          <SecretBox label={t("settings.api.issued.token")} value={issued} hint={t("settings.api.issued.once")} />
          <SecretBox
            label={t("settings.api.issued.mcp")}
            value={`claude mcp add --transport http tolkovo ${mcpURL()} --header "Authorization: Bearer ${issued}"`}
            hint={t("settings.api.issued.mcpHint")}
          />
          <div>
            <Button variant="ghost" size="sm" onClick={() => setIssued(null)}>
              {t("settings.api.issued.hide")}
            </Button>
          </div>
        </div>
      ) : null}
    </section>
  );
}

function MyTokens() {
  const { t } = useTranslation();
  const tokens = useMyAccessTokens();
  const revoke = useRevokeMyAccessToken();
  return (
    <TokenList
      title={t("settings.api.mine.title")}
      empty={t("settings.api.mine.none")}
      query={tokens}
      revoke={revoke}
      showOwner={false}
    />
  );
}

function AllTokens() {
  const { t } = useTranslation();
  const tokens = useAccessTokens();
  const revoke = useRevokeAccessToken();
  return (
    <TokenList
      title={t("settings.api.all.title")}
      text={t("settings.api.all.text")}
      empty={t("settings.api.all.none")}
      query={tokens}
      revoke={revoke}
      showOwner
    />
  );
}

type TokenListProps = {
  title: string;
  text?: string;
  empty: string;
  query: ReturnType<typeof useMyAccessTokens>;
  revoke: ReturnType<typeof useRevokeMyAccessToken>;
  showOwner: boolean;
};

function TokenList({ title, text, empty, query, revoke, showOwner }: TokenListProps) {
  const { t } = useTranslation();
  const id = useId();
  const [notice, setNotice] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const onRevoke = (token: AccessToken) => {
    setNotice(null);
    setError(null);
    revoke.mutate(token.id, {
      onSuccess: () => setNotice(t("settings.api.row.revoked")),
      // 404: revoked a moment ago (another tab, an admin); the list refetches anyway.
      onError: (e) => setError(isApiError(e) && e.code === "not_found" ? t("settings.api.row.gone") : describeApiError(t, e).message),
    });
  };

  return (
    <section aria-labelledby={`${id}-title`} aria-busy={query.isPending} className="flex max-w-[640px] flex-col gap-3">
      <div className="flex flex-col gap-1">
        <h3 id={`${id}-title`} className="m-0 text-sm font-semibold">
          {title}
        </h3>
        {text ? <p className="m-0 text-[13px] text-ink-2">{text}</p> : null}
      </div>
      <FormMessage tone="error" message={error} />
      <FormMessage tone="success" message={notice} />
      {query.isPending ? (
        <div role="status">
          <span className="sr-only">{t("app.loading")}</span>
          <Skeleton className="h-14" />
        </div>
      ) : query.isError ? (
        <div role="alert" className="flex flex-col items-start gap-2">
          <p className="m-0 text-ink-2">{describeApiError(t, query.error).message}</p>
          <Button variant="secondary" onClick={() => void query.refetch()}>
            {t("error.retry")}
          </Button>
        </div>
      ) : query.data.length === 0 ? (
        <p className="m-0 text-[13px] text-ink-3">{empty}</p>
      ) : (
        <ul aria-labelledby={`${id}-title`} className="m-0 flex list-none flex-col divide-y divide-line rounded-xl border border-line bg-surface p-0">
          {query.data.map((token) => (
            <TokenRow key={token.id} token={token} showOwner={showOwner} disabled={revoke.isPending} onRevoke={() => onRevoke(token)} />
          ))}
        </ul>
      )}
    </section>
  );
}

function TokenRow({ token, showOwner, disabled, onRevoke }: { token: AccessToken; showOwner: boolean; disabled: boolean; onRevoke: () => void }) {
  const { t, i18n } = useTranslation();
  const fmt = new Intl.DateTimeFormat(i18n.language, { day: "numeric", month: "short", year: "numeric" });
  const date = (iso: string) => fmt.format(new Date(iso));
  const write = token.scopes.includes("tasks:write");
  return (
    <li className="flex items-center gap-3 px-3 py-2.5 max-nav:flex-wrap">
      <KeyRound aria-hidden="true" className="size-5 shrink-0 text-ink-3" strokeWidth={1.7} />
      <div className="flex min-w-0 flex-1 flex-col gap-0.5">
        <p className="m-0 flex flex-wrap items-center gap-2 text-[13px] font-medium text-ink">
          <span className="truncate">{token.name}</span>
          <code className="font-mono text-[12px] text-ink-3">{token.prefix}…</code>
          <span className="rounded-full border border-line px-2 py-px text-[11.5px] font-medium text-ink-2">
            {t(write ? "settings.api.row.write" : "settings.api.row.read")}
          </span>
        </p>
        <p className="m-0 flex flex-wrap gap-x-3 text-[12.5px] text-ink-3">
          {showOwner ? <span>{t("settings.api.row.owner", { name: token.user_name })}</span> : null}
          <span>
            {t("settings.api.row.created", { date: date(token.created_at) })},{" "}
            {token.expires_at ? t("settings.api.row.expires", { date: date(token.expires_at) }) : t("settings.api.row.noExpiry")}
          </span>
          <span>{token.last_used_at ? t("settings.api.row.used", { date: date(token.last_used_at) }) : t("settings.api.row.unused")}</span>
        </p>
      </div>
      <Button
        variant="ghost"
        size="sm"
        disabled={disabled}
        aria-label={t("settings.api.row.revokeLabel", { name: token.name })}
        onClick={onRevoke}
      >
        {t("settings.api.row.revoke")}
      </Button>
    </li>
  );
}
