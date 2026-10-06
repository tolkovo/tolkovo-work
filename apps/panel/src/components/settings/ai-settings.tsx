import type { AIProvider, AISettings } from "@tolkovo/api-client";
import { useAISettings, useCheckAISettings, useSetAISettings } from "@tolkovo/api-client/react";
import { Button, Skeleton, cn } from "@tolkovo/ui";
import { ShieldCheck } from "lucide-react";
import { useId, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

import { describeApiError } from "../../lib/api-errors";
import { FormMessage, TextField } from "../form/fields";

const PROVIDERS: readonly AIProvider[] = ["anthropic", "openai", "deepseek", "openai_compatible"];

/** Money in the API is millionths of a US dollar; the form speaks dollars. */
const MICROS = 1_000_000;
const MAX_PRICE_USD = 1000;
const MAX_BUDGET_USD = 1_000_000;

type Field = "provider" | "model" | "baseUrl" | "apiKey" | "inputPrice" | "outputPrice" | "budget";

type Draft = {
  enabled: boolean;
  provider: AIProvider | null;
  model: string;
  baseUrl: string;
  apiKey: string;
  inputPrice: string;
  outputPrice: string;
  budget: string;
};

function usd(micros: number): string {
  return String(Math.round((micros / MICROS) * 1e6) / 1e6);
}

/** "1,5" and "1.5" are the same price; empty is null; anything else is NaN. */
function parseUsd(text: string): number | null {
  const v = text.trim().replace(",", ".");
  if (v === "") return null;
  return /^\d+(\.\d+)?$/.test(v) ? Number(v) : Number.NaN;
}

function draftFrom(s: AISettings): Draft {
  return {
    enabled: s.enabled,
    provider: s.provider,
    model: s.model ?? "",
    baseUrl: s.base_url ?? "",
    apiKey: "",
    inputPrice: usd(s.prices.input_micros),
    outputPrice: usd(s.prices.output_micros),
    budget: s.monthly_budget_micros === null ? "" : usd(s.monthly_budget_micros),
  };
}

/**
 * «ИИ → Провайдер, ключ и расход» (docs/07 §7, docs/03 §8.1): the workspace's own key to one
 * model, its prices and the monthly budget. The bot, handoff and the copilot get their own
 * subsections in phase 2.
 */
export function AISettingsPage() {
  const { t } = useTranslation();
  const query = useAISettings();
  return (
    <section aria-labelledby="settings-ai-title" aria-busy={query.isPending} className="flex max-w-[640px] flex-col gap-3">
      <div className="flex flex-col gap-1">
        <h2 id="settings-ai-title" className="m-0 text-base font-semibold">
          {t("settings.ai.title")}
        </h2>
        <p className="m-0 text-[13px] text-ink-2">{t("settings.ai.lead")}</p>
      </div>
      {query.isPending ? (
        <div role="status">
          <span className="sr-only">{t("app.loading")}</span>
          <Skeleton className="h-64" />
        </div>
      ) : query.isError ? (
        <div role="alert" className="flex flex-col items-start gap-2">
          <p className="m-0 text-ink-2">{describeApiError(t, query.error).message}</p>
          <Button variant="secondary" onClick={() => void query.refetch()}>
            {t("error.retry")}
          </Button>
        </div>
      ) : (
        <>
          <Spending settings={query.data} />
          <AIForm settings={query.data} />
        </>
      )}
    </section>
  );
}

/** This month's spending against the budget; a meter only when there is a budget. */
function Spending({ settings }: { settings: AISettings }) {
  const { t, i18n } = useTranslation();
  const money = (micros: number) => {
    if (micros > 0 && micros < 10_000) return t("settings.ai.lessThanCent");
    return new Intl.NumberFormat(i18n.language, { style: "currency", currency: "USD" }).format(micros / MICROS);
  };
  const since = new Intl.DateTimeFormat(i18n.language, { day: "numeric", month: "long", timeZone: "UTC" }).format(
    new Date(settings.period_start),
  );
  const budget = settings.monthly_budget_micros;
  const share = budget ? Math.min(1, settings.spent_micros / budget) : 0;
  const pricesUnset = settings.prices.input_micros === 0 && settings.prices.output_micros === 0;
  return (
    <div className="flex flex-col gap-2 rounded-xl border border-line bg-surface p-4">
      <p className="m-0 text-[13px] text-ink">
        {budget === null
          ? t("settings.ai.spentNoBudget", { since, spent: money(settings.spent_micros) })
          : t("settings.ai.spent", { since, spent: money(settings.spent_micros), budget: money(budget) })}
      </p>
      {budget !== null ? (
        <>
          <div
            role="meter"
            aria-label={t("settings.ai.budgetUsed")}
            aria-valuemin={0}
            aria-valuemax={100}
            aria-valuenow={Math.round(share * 100)}
            className="h-1.5 overflow-hidden rounded-full bg-hover"
          >
            <div className={cn("h-full rounded-full", share >= 0.8 ? "bg-lamp" : "bg-teal")} style={{ width: `${share * 100}%` }} />
          </div>
          {share >= 1 ? (
            <p className="m-0 text-[12.5px] text-danger">{t("settings.ai.budgetSpent")}</p>
          ) : share >= 0.8 ? (
            <p className="m-0 text-[12.5px] text-ink-2">{t("settings.ai.budgetNear")}</p>
          ) : null}
        </>
      ) : null}
      {pricesUnset && settings.provider ? <p className="m-0 text-[12.5px] text-ink-2">{t("settings.ai.pricesUnset")}</p> : null}
    </div>
  );
}

function AIForm({ settings }: { settings: AISettings }) {
  const { t } = useTranslation();
  const save = useSetAISettings();
  const check = useCheckAISettings();
  const uid = useId();
  const id = (name: string) => `ai-${name}-${uid}`;
  const [draft, setDraft] = useState<Draft>(() => draftFrom(settings));
  const [errors, setErrors] = useState<Partial<Record<Field, string>>>({});
  const [message, setMessage] = useState<{ tone: "success" | "error"; text: string } | null>(null);
  const [checked, setChecked] = useState<{ tone: "success" | "error"; text: string } | null>(null);
  const set = (patch: Partial<Draft>) => setDraft((d) => ({ ...d, ...patch }));

  const models = settings.models.filter((m) => m.provider === draft.provider);
  const keepsKey = settings.key !== null && draft.provider === settings.provider;
  const losesKey = settings.key !== null && draft.provider !== settings.provider;
  const keyOptional = draft.provider === "openai_compatible";

  const chooseModel = (model: string) => {
    const known = models.find((m) => m.model === model);
    set(known ? { model, inputPrice: usd(known.prices.input_micros), outputPrice: usd(known.prices.output_micros) } : { model });
  };

  const onSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setMessage(null);
    setChecked(null);
    const found: Partial<Record<Field, string>> = {};
    const model = draft.model.trim();
    const baseUrl = draft.baseUrl.trim();
    const apiKey = draft.apiKey.trim();
    const input = parseUsd(draft.inputPrice) ?? 0;
    const output = parseUsd(draft.outputPrice) ?? 0;
    const budget = parseUsd(draft.budget);
    if (!draft.provider && (draft.enabled || model || apiKey)) found.provider = t("settings.ai.providerRequired");
    if (draft.enabled && !model) found.model = t("settings.ai.modelRequired");
    if (model && !/^[A-Za-z0-9][A-Za-z0-9._:/@+-]{0,99}$/.test(model)) found.model = t("settings.ai.modelInvalid");
    if (draft.provider === "openai_compatible" && !/^https?:\/\/\S+$/.test(baseUrl)) found.baseUrl = t("settings.ai.baseUrlInvalid");
    if (/\s/.test(apiKey)) found.apiKey = t("settings.ai.keyInvalid");
    if (draft.enabled && draft.provider && !keyOptional && !apiKey && !keepsKey) found.apiKey = t("settings.ai.keyRequired");
    if (Number.isNaN(input) || input > MAX_PRICE_USD) found.inputPrice = t("settings.ai.priceInvalid");
    if (Number.isNaN(output) || output > MAX_PRICE_USD) found.outputPrice = t("settings.ai.priceInvalid");
    if (budget !== null && (Number.isNaN(budget) || budget > MAX_BUDGET_USD)) found.budget = t("settings.ai.budgetInvalid");
    setErrors(found);
    const first = (["provider", "model", "baseUrl", "apiKey", "inputPrice", "outputPrice", "budget"] as const).find((f) => found[f]);
    if (first) {
      document.getElementById(first === "provider" ? id("provider-anthropic") : id(first))?.focus();
      return;
    }
    save.mutate(
      {
        enabled: draft.enabled,
        provider: draft.provider,
        model: model || null,
        base_url: draft.provider === "openai_compatible" ? baseUrl : null,
        ...(apiKey ? { api_key: apiKey } : {}),
        prices: { input_micros: Math.round(input * MICROS), output_micros: Math.round(output * MICROS) },
        monthly_budget_micros: budget === null ? null : Math.round(budget * MICROS),
      },
      {
        onSuccess: (next) => {
          setDraft(draftFrom(next));
          setMessage({ tone: "success", text: t("settings.ai.saved") });
        },
        onError: (e) => {
          const described = describeApiError<Field>(t, e, { ai_key_required: "apiKey" });
          if (described.field) setErrors({ [described.field]: described.message });
          setMessage({ tone: "error", text: described.message });
        },
      },
    );
  };

  const runCheck = () => {
    setChecked(null);
    check.mutate(undefined, {
      onSuccess: (r) => setChecked({ tone: "success", text: t("settings.ai.checkOk", { model: r.model, ms: r.latency_ms }) }),
      onError: (e) => setChecked({ tone: "error", text: describeApiError(t, e).message }),
    });
  };

  const busy = save.isPending || check.isPending;
  return (
    <form onSubmit={onSubmit} noValidate aria-busy={save.isPending} className="flex flex-col gap-4 rounded-xl border border-line bg-surface p-4">
      {!settings.encryption_available ? (
        <p role="alert" className="m-0 rounded-lg border border-line bg-lamp-soft px-3 py-2 text-[12.5px] text-ink">
          {t("settings.ai.noEncryption")}
        </p>
      ) : null}
      <fieldset disabled={save.isPending} className="m-0 flex flex-col gap-4 border-0 p-0">
        <div className="flex items-start gap-2.5">
          <input
            id={id("enabled")}
            type="checkbox"
            checked={draft.enabled}
            onChange={(e) => set({ enabled: e.target.checked })}
            aria-describedby={`${id("enabled")}-hint`}
            className="mt-0.5 size-4 shrink-0 accent-teal"
          />
          <div className="flex min-w-0 flex-col gap-0.5">
            <label htmlFor={id("enabled")} className="text-[13px] text-ink">
              {t("settings.ai.enabled")}
            </label>
            <p id={`${id("enabled")}-hint`} className="m-0 text-[12.5px] text-ink-3">
              {t("settings.ai.enabledHint")}
            </p>
          </div>
        </div>

        <fieldset className="m-0 flex flex-col gap-2 border-0 p-0" aria-describedby={errors.provider ? `${id("provider")}-error` : undefined}>
          <legend className="mb-1 p-0 text-[13px] font-medium text-ink">{t("settings.ai.provider")}</legend>
          {PROVIDERS.map((p) => {
            const pid = id(`provider-${p}`);
            const on = draft.provider === p;
            return (
              <label
                key={p}
                htmlFor={pid}
                className={cn(
                  "flex cursor-pointer items-start gap-2.5 rounded-lg border px-3 py-2.5",
                  on ? "border-teal bg-teal-soft" : "border-line hover:bg-hover",
                )}
              >
                <input
                  id={pid}
                  type="radio"
                  name="ai-provider"
                  value={p}
                  checked={on}
                  onChange={() => set({ provider: p, model: "", inputPrice: "0", outputPrice: "0", apiKey: "" })}
                  aria-labelledby={`${pid}-label`}
                  aria-describedby={`${pid}-hint`}
                  className="mt-0.5 size-4 shrink-0 accent-teal"
                />
                <span className="flex min-w-0 flex-col gap-0.5">
                  <span id={`${pid}-label`} className="text-[13px] text-ink">
                    {t(`settings.ai.providers.${p}`)}
                  </span>
                  <span id={`${pid}-hint`} className="text-[12.5px] text-ink-2">
                    {t(`settings.ai.providers.${p}Hint`)}
                  </span>
                </span>
              </label>
            );
          })}
          {errors.provider ? (
            <p id={`${id("provider")}-error`} className="m-0 text-[12.5px] text-danger">
              {errors.provider}
            </p>
          ) : null}
        </fieldset>

        {draft.provider ? (
          <>
            {draft.provider === "openai_compatible" ? (
              <TextField
                id={id("baseUrl")}
                name="base_url"
                type="url"
                label={t("settings.ai.baseUrl")}
                hint={t("settings.ai.baseUrlHint")}
                error={errors.baseUrl}
                value={draft.baseUrl}
                onChange={(e) => set({ baseUrl: e.target.value })}
                placeholder={t("settings.ai.baseUrlPlaceholder")}
                autoComplete="off"
                spellCheck={false}
                className="font-mono"
              />
            ) : null}
            <TextField
              id={id("apiKey")}
              name="api_key"
              type="password"
              label={keyOptional ? t("settings.ai.keyOptional") : t("settings.ai.key")}
              hint={
                keepsKey && settings.key
                  ? t("settings.ai.keySaved", { hint: settings.key.hint })
                  : losesKey
                    ? t("settings.ai.keyLost")
                    : t("settings.ai.keyHint")
              }
              error={errors.apiKey}
              value={draft.apiKey}
              onChange={(e) => set({ apiKey: e.target.value })}
              placeholder={keepsKey ? t("settings.ai.keyKeep") : ""}
              disabled={!settings.encryption_available}
              autoComplete="off"
              spellCheck={false}
              maxLength={500}
              className="font-mono"
            />
            <TextField
              id={id("model")}
              name="model"
              label={t("settings.ai.model")}
              hint={t("settings.ai.modelHint")}
              error={errors.model}
              value={draft.model}
              onChange={(e) => chooseModel(e.target.value)}
              list={models.length > 0 ? id("models") : undefined}
              autoComplete="off"
              spellCheck={false}
              maxLength={100}
              className="font-mono"
            />
            {models.length > 0 ? (
              <datalist id={id("models")}>
                {models.map((m) => (
                  <option key={m.model} value={m.model} />
                ))}
              </datalist>
            ) : null}
            <fieldset className="m-0 flex flex-col gap-2 border-0 p-0">
              <legend className="mb-1 p-0 text-[13px] font-medium text-ink">{t("settings.ai.prices")}</legend>
              <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                <TextField
                  id={id("inputPrice")}
                  name="input_price"
                  label={t("settings.ai.inputPrice")}
                  error={errors.inputPrice}
                  value={draft.inputPrice}
                  onChange={(e) => set({ inputPrice: e.target.value })}
                  inputMode="decimal"
                  autoComplete="off"
                />
                <TextField
                  id={id("outputPrice")}
                  name="output_price"
                  label={t("settings.ai.outputPrice")}
                  error={errors.outputPrice}
                  value={draft.outputPrice}
                  onChange={(e) => set({ outputPrice: e.target.value })}
                  inputMode="decimal"
                  autoComplete="off"
                />
              </div>
              <p className="m-0 text-[12.5px] text-ink-3">{t("settings.ai.pricesHint")}</p>
            </fieldset>
            <TextField
              id={id("budget")}
              name="budget"
              label={t("settings.ai.budget")}
              hint={t("settings.ai.budgetHint")}
              error={errors.budget}
              value={draft.budget}
              onChange={(e) => set({ budget: e.target.value })}
              inputMode="decimal"
              autoComplete="off"
            />
          </>
        ) : null}
      </fieldset>

      <p className="m-0 flex items-start gap-2 text-[12.5px] text-ink-2">
        <ShieldCheck aria-hidden="true" className="mt-px size-4 shrink-0 text-teal" strokeWidth={1.75} />
        {t("settings.ai.privacy")}
      </p>

      <div className="flex flex-wrap items-center gap-2">
        <Button type="submit" disabled={busy}>
          {t("settings.ai.save")}
        </Button>
        {settings.provider && settings.model ? (
          <Button type="button" variant="secondary" disabled={busy} onClick={runCheck}>
            {check.isPending ? t("settings.ai.checking") : t("settings.ai.check")}
          </Button>
        ) : null}
      </div>
      {settings.provider && settings.model ? <p className="m-0 text-[12.5px] text-ink-3">{t("settings.ai.checkHint")}</p> : null}
      <FormMessage tone="success" message={message?.tone === "success" ? message.text : null} />
      <FormMessage tone="error" message={message?.tone === "error" ? message.text : null} />
      <FormMessage tone="success" message={checked?.tone === "success" ? checked.text : null} />
      <FormMessage tone="error" message={checked?.tone === "error" ? checked.text : null} />
    </form>
  );
}
