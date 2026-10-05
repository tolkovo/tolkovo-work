import type { OperatorStatus } from "@tolkovo/api-client";
import { useCan, useMyPresence, useSetMyPresence } from "@tolkovo/api-client/react";
import { ChevronDown } from "lucide-react";
import { useTranslation } from "react-i18next";

import { Menu } from "../menu";
import { PresenceDot } from "../presence-dot";

const STATUSES: readonly OperatorStatus[] = ["online", "away"];

/**
 * The operator's status in the top bar (docs/07 §3): «Онлайн» accepts conversations from the
 * router, «Отошёл» does not; capacity joins it with lines (phase 3). Only members who work
 * the inbox see it.
 */
export function StatusSwitch() {
  const { t } = useTranslation();
  const canWork = useCan("conversations.reply");
  const presence = useMyPresence();
  const set = useSetMyPresence();

  if (!canWork || !presence.data) return null;
  const current = set.isPending && set.variables ? set.variables : presence.data.status;

  return (
    <Menu
      label={t("topbar.status.label", { status: t(`topbar.status.${current}`) })}
      disabled={set.isPending}
      trigger={
        <>
          <PresenceDot presence={current} />
          <span className="max-nav:sr-only">{t(`topbar.status.${current}`)}</span>
          <ChevronDown aria-hidden="true" className="size-3.5 text-ink-3" />
        </>
      }
      triggerClassName="flex h-8 items-center gap-1.5 rounded-md px-2 text-[13px] text-ink hover:bg-hover disabled:opacity-55"
      items={STATUSES.map((s) => ({
        label: t(`topbar.status.${s}`),
        detail: t(`topbar.status.${s}Hint`),
        disabled: s === current,
        onSelect: () => set.mutate(s),
      }))}
    />
  );
}
