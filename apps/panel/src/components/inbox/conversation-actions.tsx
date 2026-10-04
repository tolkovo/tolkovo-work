import { assignConversation, changeConversationStatus, isApiError, type Conversation } from "@tolkovo/api-client";
import { useMembers, useSession } from "@tolkovo/api-client/react";
import { Button } from "@tolkovo/ui";
import { Check, ChevronDown, Ellipsis } from "lucide-react";
import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";

import { isConflict, useAbilities, type ConversationChange } from "../../lib/inbox/actions";
import { Menu, type MenuItem } from "../menu";

/**
 * Actions of the conversation header (docs/07 §4): at most three visible — Take, Transfer,
 * Close — and the rest under ⋯. Escalation arrives with lines (phase 3). Every change sends the
 * version the operator saw; a conflict refetches and asks to retry.
 */
export function ConversationActions({ conversation: c, change }: { conversation: Conversation; change: ConversationChange }) {
  const { t } = useTranslation();
  const can = useAbilities(c);
  const { data: session } = useSession();
  const { data: members } = useMembers();

  const status = (to: Conversation["status"]) => () =>
    change.mutate((api) => changeConversationStatus(api, c.id, to, c.version));
  const assign = (userId: string | null) => () =>
    change.mutate((api) => assignConversation(api, c.id, userId, c.version));

  const targets = (members ?? []).filter((m) => m.active && m.user_id !== c.assignee_id);
  const more: MenuItem[] = [
    ...(can.wait ? [{ label: t("inbox.actions.wait"), onSelect: status("pending") }] : []),
    ...(can.resume ? [{ label: t("inbox.actions.resume"), onSelect: status("assigned") }] : []),
    ...(can.release ? [{ label: t("inbox.actions.release"), onSelect: assign(null) }] : []),
  ];

  return (
    <div className="flex shrink-0 items-center gap-1.5">
      {can.take && session && (
        <Button size="sm" variant="primary" disabled={change.isPending} onClick={assign(session.user.id)}>
          {t("inbox.actions.take")}
        </Button>
      )}
      {can.reopen && (
        <Button size="sm" disabled={change.isPending} onClick={status("assigned")}>
          {t("inbox.actions.reopen")}
        </Button>
      )}
      {can.transfer && targets.length > 0 && (
        <Menu
          label={t("inbox.actions.transfer")}
          disabled={change.isPending}
          trigger={
            <>
              {t("inbox.actions.transfer")}
              <ChevronDown aria-hidden="true" className="size-3.5" />
            </>
          }
          triggerClassName="inline-flex h-[26px] items-center gap-1 rounded-md border border-line bg-surface px-2.5 text-[13px] font-medium text-ink hover:bg-hover disabled:opacity-55"
          items={targets.map((m) => ({
            label: m.user_id === session?.user.id ? `${m.name} (${t("inbox.you")})` : m.name,
            onSelect: assign(m.user_id),
          }))}
        />
      )}
      {can.close && (
        <Button size="sm" disabled={change.isPending} onClick={status("resolved")}>
          <Check aria-hidden="true" />
          {t("inbox.actions.close")}
        </Button>
      )}
      {more.length > 0 && (
        <Menu
          label={t("inbox.actions.more")}
          disabled={change.isPending}
          trigger={<Ellipsis aria-hidden="true" className="size-4" />}
          triggerClassName="grid size-[26px] place-items-center rounded-md text-ink-2 hover:bg-hover disabled:opacity-55"
          items={more}
        />
      )}
    </div>
  );
}

/** The failure of the last action, visible under the header. */
export function ActionErrorLine({ error, onDismiss }: { error: unknown; onDismiss: () => void }) {
  const { t } = useTranslation();
  return (
    <div role="alert" className="flex items-center gap-2 border-b border-line bg-danger-soft px-4 py-1.5 text-[13px] text-ink">
      <span className="mr-auto">{actionError(error, t)}</span>
      <Button size="sm" variant="ghost" onClick={onDismiss}>
        {t("inbox.actions.dismiss")}
      </Button>
    </div>
  );
}

export function actionError(error: unknown, t: TFunction): string {
  if (isConflict(error)) return t("inbox.actions.conflict");
  if (isApiError(error)) {
    if (error.code === "not_assignee") return t("inbox.actions.notAssignee");
    if (error.code === "invalid_transition") return t("inbox.actions.invalid");
    if (error.status === 0) return t("inbox.composer.errorOffline");
  }
  return t("inbox.actions.error");
}
