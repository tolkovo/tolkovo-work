import type { Member } from "@tolkovo/api-client";
import { useDeactivateMember, useUpdateMember } from "@tolkovo/api-client/react";
import { Button, Select } from "@tolkovo/ui";
import { useEffect, useId, useRef, useState } from "react";
import { useTranslation } from "react-i18next";

import { describeApiError } from "../../lib/api-errors";
import { ASSIGNABLE_ROLES, answersCustomers, type AssignableRole } from "../../lib/team";
import { FormMessage } from "../form/fields";

/** The role of a member, changed in place by an admin (ADR-0020). */
export function MemberRole({ member, onDone }: { member: Member; onDone: (notice: string) => void }) {
  const { t } = useTranslation();
  const update = useUpdateMember();
  return (
    <Select
      aria-label={t("settings.team.roleOf", { name: member.name })}
      value={member.role}
      disabled={update.isPending}
      className="h-8 w-auto text-[13px]"
      onChange={(e) => {
        const role = e.currentTarget.value as AssignableRole;
        update.mutate(
          { id: member.user_id, body: { role } },
          {
            onSuccess: (m) => onDone(t("settings.team.roleChanged", { name: m.name, role: t(`roles.${m.role}`) })),
            onError: (err) => onDone(describeApiError(t, err).message),
          },
        );
      }}
    >
      {ASSIGNABLE_ROLES.map((r) => (
        <option key={r} value={r}>
          {t(`roles.${r}`)}
        </option>
      ))}
    </Select>
  );
}

/** Opens the dialog that removes a member from the team. */
export function RemoveMember({ member, members, onDone }: { member: Member; members: Member[]; onDone: (notice: string) => void }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const opener = useRef<HTMLButtonElement>(null);
  const close = () => {
    setOpen(false);
    opener.current?.focus();
  };
  return (
    <>
      <Button ref={opener} size="sm" aria-label={t("settings.team.removeLabel", { name: member.name })} onClick={() => setOpen(true)}>
        {t("settings.team.remove")}
      </Button>
      {open ? (
        <RemoveDialog
          member={member}
          members={members}
          onCancel={close}
          onRemoved={() => {
            close();
            onDone(t("settings.team.removed", { name: member.name }));
          }}
        />
      ) : null}
    </>
  );
}

/**
 * Asks who takes the member's open conversations and tasks, then removes them. Escape and a
 * click outside cancel.
 */
function RemoveDialog({
  member,
  members,
  onCancel,
  onRemoved,
}: {
  member: Member;
  members: Member[];
  onCancel: () => void;
  onRemoved: () => void;
}) {
  const { t } = useTranslation();
  const titleId = useId();
  const textId = useId();
  const convsId = useId();
  const tasksId = useId();
  const first = useRef<HTMLSelectElement>(null);
  const deactivate = useDeactivateMember();
  const [convsTo, setConvsTo] = useState("");
  const [tasksTo, setTasksTo] = useState("");
  useEffect(() => first.current?.focus(), []);
  const others = members.filter((m) => m.active && m.user_id !== member.user_id);
  const error = deactivate.isError ? describeApiError(t, deactivate.error).message : null;
  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-labelledby={titleId}
      aria-describedby={textId}
      className="fixed inset-0 z-50 grid place-items-center bg-ink/35 p-4"
      onKeyDown={(e) => {
        if (e.key === "Escape") {
          e.preventDefault();
          onCancel();
        }
      }}
      onClick={(e) => {
        if (e.target === e.currentTarget) onCancel();
      }}
    >
      <form
        className="flex w-full max-w-105 flex-col gap-3 rounded-2xl border border-line bg-surface p-4.5 text-ink shadow-pop"
        onSubmit={(e) => {
          e.preventDefault();
          deactivate.mutate(
            { id: member.user_id, body: { conversations_to: convsTo || null, tasks_to: tasksTo || null } },
            { onSuccess: onRemoved },
          );
        }}
      >
        <h2 id={titleId} className="m-0 text-[15.5px] font-semibold">
          {t("settings.team.removeDialog.title", { name: member.name })}
        </h2>
        <p id={textId} className="m-0 text-[13px] text-ink-2">
          {t("settings.team.removeDialog.text")}
        </p>
        <label htmlFor={convsId} className="text-[13px] font-medium">
          {t("settings.team.removeDialog.conversations")}
        </label>
        <Select id={convsId} ref={first} value={convsTo} onChange={(e) => setConvsTo(e.currentTarget.value)}>
          <option value="">{t("settings.team.removeDialog.toQueue")}</option>
          {others
            .filter((m) => answersCustomers(m.role))
            .map((m) => (
              <option key={m.user_id} value={m.user_id}>
                {m.name}
              </option>
            ))}
        </Select>
        <label htmlFor={tasksId} className="text-[13px] font-medium">
          {t("settings.team.removeDialog.tasks")}
        </label>
        <Select id={tasksId} value={tasksTo} onChange={(e) => setTasksTo(e.currentTarget.value)}>
          <option value="">{t("settings.team.removeDialog.toNobody")}</option>
          {others.map((m) => (
            <option key={m.user_id} value={m.user_id}>
              {m.name}
            </option>
          ))}
        </Select>
        <FormMessage tone="error" message={error} />
        <div className="flex justify-end gap-2">
          <Button type="button" size="sm" onClick={onCancel}>
            {t("settings.team.removeDialog.cancel")}
          </Button>
          <Button type="submit" size="sm" variant="destructive" disabled={deactivate.isPending}>
            {deactivate.isPending ? t("settings.team.removeDialog.pending") : t("settings.team.removeDialog.submit")}
          </Button>
        </div>
      </form>
    </div>
  );
}
