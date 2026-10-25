import type { RoutingMode, RoutingSettings } from "@tolkovo/api-client";
import { useMembers, useRouting, useRoutingMembers, useSetRouting, useSetRoutingMember } from "@tolkovo/api-client/react";
import { Button, Select, Skeleton, cn } from "@tolkovo/ui";
import { useId, useState } from "react";
import { useTranslation } from "react-i18next";

import { describeApiError } from "../../lib/api-errors";
import { answersCustomers } from "../../lib/team";
import { FormMessage } from "../form/fields";
import { LineSettings } from "./line-settings";
import { PresenceDot } from "../presence-dot";

const MODES: readonly RoutingMode[] = ["manual", "round_robin", "least_loaded"];

/** The limits offered: the server takes 1–50. */
const LIMITS = [1, 2, 3, 4, 5, 6, 7, 8, 10, 12, 15, 20, 30, 50] as const;

function LimitOptions({ none }: { none: string }) {
  return (
    <>
      <option value="">{none}</option>
      {LIMITS.map((n) => (
        <option key={n} value={n}>
          {n}
        </option>
      ))}
    </>
  );
}

const limitOf = (value: string) => (value === "" ? null : Number(value));

/**
 * Capacity (ADR-0021): how many conversations in work the router gives each operator, the
 * workspace's limit and each member's own, with their load now.
 */
function Capacity({ settings, onSave, saving }: { settings: RoutingSettings; onSave: (next: RoutingSettings) => void; saving: boolean }) {
  const { t } = useTranslation();
  const id = useId();
  const { data: members } = useMembers();
  const limits = useRoutingMembers();
  const setOwn = useSetRoutingMember();
  const [error, setError] = useState<string | null>(null);
  const names = new Map(members?.map((m) => [m.user_id, m]) ?? []);
  const fallback = settings.max_chats;
  return (
    <div className="flex flex-col gap-3 border-t border-line pt-3">
      <div className="flex flex-col gap-1">
        <label htmlFor={id} className="text-[13px] font-medium text-ink">
          {t("settings.routing.maxChats")}
        </label>
        <div className="w-40">
          <Select
            id={id}
            aria-describedby={`${id}-hint`}
            value={fallback === null ? "" : String(fallback)}
            disabled={saving}
            onChange={(e) => onSave({ ...settings, max_chats: limitOf(e.currentTarget.value) })}
          >
            <LimitOptions none={t("settings.routing.noLimit")} />
          </Select>
        </div>
        <p id={`${id}-hint`} className="m-0 text-[12.5px] text-ink-2">
          {t("settings.routing.maxChatsHint")}
        </p>
      </div>
      {limits.data && limits.data.length > 0 ? (
        <table className="w-full table-fixed border-collapse text-[13px]">
          <caption className="mb-1 text-left text-[13px] font-medium text-ink">{t("settings.routing.members")}</caption>
          <colgroup>
            <col />
            <col className="w-24 max-nav:w-16" />
            <col className="w-40 max-nav:w-28" />
          </colgroup>
          <thead>
            <tr className="text-left text-[12px] text-ink-2">
              <th scope="col" className="py-1 font-normal">
                {t("settings.routing.member")}
              </th>
              <th scope="col" className="py-1 font-normal">
                {t("settings.routing.inWork")}
              </th>
              <th scope="col" className="py-1 font-normal">
                {t("settings.routing.ownLimit")}
              </th>
            </tr>
          </thead>
          <tbody>
            {limits.data.map((l) => {
              const m = names.get(l.user_id);
              const max = l.max_chats ?? fallback;
              const full = max !== null && l.open >= max;
              return (
                <tr key={l.user_id} className="border-t border-line">
                  <th scope="row" className="truncate py-1.5 pr-2 text-left font-normal text-ink">
                    <span className="inline-flex min-w-0 items-center gap-1.5">
                      {m ? <PresenceDot presence={m.presence} /> : null}
                      <span className="truncate">{m?.name ?? l.user_id}</span>
                    </span>
                  </th>
                  <td className={cn("py-1.5 pr-2 tabular-nums whitespace-nowrap", full ? "font-medium text-ink" : "text-ink-2")}>
                    {max === null ? l.open : t("settings.routing.load", { open: l.open, max })}
                  </td>
                  <td className="py-1.5">
                    <Select
                      aria-label={t("settings.routing.ownLimitOf", { name: m?.name ?? "" })}
                      value={l.max_chats === null ? "" : String(l.max_chats)}
                      disabled={setOwn.isPending}
                      onChange={(e) => {
                        setError(null);
                        setOwn.mutate(
                          { userId: l.user_id, maxChats: limitOf(e.currentTarget.value) },
                          { onError: (err) => setError(describeApiError(t, err).message) },
                        );
                      }}
                    >
                      <LimitOptions none={t("settings.routing.asEveryone")} />
                    </Select>
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      ) : null}
      <FormMessage tone="error" message={error} />
    </div>
  );
}

/** Who takes conversations now: the members the router would give one to. */
function OnlineNow() {
  const { t } = useTranslation();
  const { data: members } = useMembers();
  if (!members) return null;
  const online = members.filter((m) => m.active && answersCustomers(m.role) && m.presence === "online");
  return (
    <p className="m-0 flex flex-wrap items-center gap-x-3 gap-y-1 text-[13px] text-ink-2">
      {online.length === 0
        ? t("settings.routing.nobodyOnline")
        : t("settings.routing.onlineNow", { count: online.length })}
      {online.map((m) => (
        <span key={m.user_id} className="inline-flex items-center gap-1.5 text-ink">
          <PresenceDot presence="online" />
          {m.name}
        </span>
      ))}
    </p>
  );
}

/**
 * «Распределение» (docs/07 §7, docs/04 §5.2, ADR-0021): how queued conversations reach operators
 * and how many each may have in work. Skills and offers with a timeout come later.
 */
export function RoutingSettingsPage() {
  const { t } = useTranslation();
  const query = useRouting();
  const update = useSetRouting();
  const [saved, setSaved] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const save = (next: RoutingSettings) => {
    setSaved(null);
    setError(null);
    update.mutate(next, {
      onSuccess: () => setSaved(t("settings.routing.saved")),
      onError: (e) => setError(describeApiError(t, e).message),
    });
  };

  return (
    <section aria-labelledby="settings-routing-title" aria-busy={query.isPending} className="flex max-w-[640px] flex-col gap-3">
      <h2 id="settings-routing-title" className="m-0 text-base font-semibold">
        {t("settings.routing.title")}
      </h2>
      {query.isPending ? (
        <div role="status">
          <span className="sr-only">{t("app.loading")}</span>
          <Skeleton className="h-32" />
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
          <fieldset className="m-0 flex flex-col gap-2 border-0 p-0">
            <legend className="mb-1 p-0 text-[13px] font-medium text-ink">{t("settings.routing.mode")}</legend>
            {MODES.map((mode) => {
              const id = `routing-mode-${mode}`;
              const checked = (update.isPending && update.variables ? update.variables.mode : query.data.mode) === mode;
              return (
                <label
                  key={mode}
                  htmlFor={id}
                  className={cn(
                    "flex cursor-pointer items-start gap-2.5 rounded-lg border px-3 py-2.5",
                    checked ? "border-teal bg-teal-soft" : "border-line hover:bg-hover",
                  )}
                >
                  <input
                    id={id}
                    type="radio"
                    name="routing-mode"
                    value={mode}
                    checked={checked}
                    disabled={update.isPending}
                    onChange={() => save({ ...query.data, mode })}
                    aria-labelledby={`${id}-label`}
                    aria-describedby={`${id}-hint`}
                    className="mt-0.5 size-4 shrink-0 accent-teal"
                  />
                  <span className="flex min-w-0 flex-col gap-0.5">
                    <span id={`${id}-label`} className="text-[13px] text-ink">
                      {t(`settings.routing.modes.${mode}`)}
                    </span>
                    <span id={`${id}-hint`} className="text-[12.5px] text-ink-2">
                      {t(`settings.routing.modes.${mode}Hint`)}
                    </span>
                  </span>
                </label>
              );
            })}
          </fieldset>
          {query.data.mode !== "manual" ? (
            <>
              <p className="m-0 text-[12.5px] text-ink-2">{t("settings.routing.rules")}</p>
              <OnlineNow />
              <Capacity settings={query.data} onSave={save} saving={update.isPending} />
            </>
          ) : null}
          <FormMessage tone="error" message={error} />
          <FormMessage tone="success" message={saved} />
        </div>
      )}
      <LineSettings />
    </section>
  );
}
