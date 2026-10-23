import type { RoutingMode } from "@tolkovo/api-client";
import { useMembers, useRouting, useSetRouting } from "@tolkovo/api-client/react";
import { Button, Skeleton, cn } from "@tolkovo/ui";
import { useState } from "react";
import { useTranslation } from "react-i18next";

import { describeApiError } from "../../lib/api-errors";
import { answersCustomers } from "../../lib/team";
import { FormMessage } from "../form/fields";
import { PresenceDot } from "../presence-dot";

const MODES: readonly RoutingMode[] = ["manual", "round_robin"];

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
 * «Распределение» (docs/07 §7, docs/04 §5.2): how queued conversations reach operators. Phase 1
 * has two ways; lines, capacity, skills and offers with a timeout come with phase 3.
 */
export function RoutingSettingsPage() {
  const { t } = useTranslation();
  const query = useRouting();
  const update = useSetRouting();
  const [saved, setSaved] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const save = (mode: RoutingMode) => {
    setSaved(null);
    setError(null);
    update.mutate(mode, {
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
              const checked = (update.isPending && update.variables ? update.variables : query.data.mode) === mode;
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
                    onChange={() => save(mode)}
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
          {query.data.mode === "round_robin" ? (
            <>
              <p className="m-0 text-[12.5px] text-ink-2">{t("settings.routing.rules")}</p>
              <OnlineNow />
            </>
          ) : null}
          <FormMessage tone="error" message={error} />
          <FormMessage tone="success" message={saved} />
        </div>
      )}
    </section>
  );
}
