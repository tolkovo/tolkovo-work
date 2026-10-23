import type { Member } from "@tolkovo/api-client";
import { useDeactivateMember, useUpdateMember } from "@tolkovo/api-client/react";
import { Button, Select } from "@tolkovo/ui";
import { useEffect, useId, useRef, useState } from "react";
import { useTranslation } from "react-i18next";

import { describeApiError } from "../../lib/api-errors";
import { ASSIGNABLE_ROLES, answersCustomers, type AssignableRole } from "../../lib/team";
import { FormMessage } from "../form/fields";

/**
 * The role of a member, changed in place by an admin (ADR-0020). A member who answers customers
 * and becomes a programmer gives up their open conversations: a dialog asks to whom.
 */
export function MemberRole({ member, members, onDone }: { member: Member; members: Member[]; onDone: (notice: string) => void }) {
  const { t } = useTranslation();
  const update = useUpdateMember();
  const select = useRef<HTMLSelectElement>(null);
  const [asking, setAsking] = useState<AssignableRole | null>(null);
  const change = (role: AssignableRole, conversationsTo: string | null, after?: () => void) =>
    update.mutate(
      { id: member.user_id, body: conversationsTo ? { role, conversations_to: conversationsTo } : { role } },
      {
        onSuccess: (m) => {
          after?.();
          onDone(t("settings.team.roleChanged", { name: m.name, role: t(`roles.${m.role}`) }));
        },
        onError: (err) => {
          if (!after) onDone(describeApiError(t, err).message);
        },
      },
    );
  const close = () => {
    setAsking(null);
    update.reset();
    select.current?.focus();
  };
  return (
    <>
      <Select
        ref={select}
        aria-label={t("settings.team.roleOf", { name: member.name })}
        value={member.role}
        disabled={update.isPending}
        className="h-8 w-auto text-[13px]"
        onChange={(e) => {
          const role = e.currentTarget.value as AssignableRole;
          if (answersCustomers(member.role) && !answersCustomers(role)) setAsking(role);
          else change(role, null);
        }}
      >
        {ASSIGNABLE_ROLES.map((r) => (
          <option key={r} value={r}>
            {t(`roles.${r}`)}
          </option>
        ))}
      </Select>
      {asking ? (
        <HandOverDialog
          title={t("settings.team.roleDialog.title", { name: member.name, role: t(`roles.${asking}`) })}
          text={t("settings.team.roleDialog.text")}
          submit={t("settings.team.roleDialog.submit")}
          pending={update.isPending}
          error={update.isError ? describeApiError(t, update.error).message : null}
          member={member}
          members={members}
          tasks={false}
          onCancel={close}
          onSubmit={(convsTo) => change(asking, convsTo, () => setAsking(null))}
        />
      ) : null}
    </>
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

/** Removes the member from the team once the admin chose who takes their work. */
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
  const deactivate = useDeactivateMember();
  return (
    <HandOverDialog
      title={t("settings.team.removeDialog.title", { name: member.name })}
      text={t("settings.team.removeDialog.text")}
      submit={deactivate.isPending ? t("settings.team.removeDialog.pending") : t("settings.team.removeDialog.submit")}
      pending={deactivate.isPending}
      error={deactivate.isError ? describeApiError(t, deactivate.error).message : null}
      member={member}
      members={members}
      tasks
      onCancel={onCancel}
      onSubmit={(convsTo, tasksTo) =>
        deactivate.mutate({ id: member.user_id, body: { conversations_to: convsTo, tasks_to: tasksTo } }, { onSuccess: onRemoved })
      }
    />
  );
}

/**
 * Asks who takes the member's open conversations, and their open tasks too when they leave the
 * team. Escape and a click outside cancel.
 */
function HandOverDialog({
  title,
  text,
  submit,
  pending,
  error,
  member,
  members,
  tasks,
  onCancel,
  onSubmit,
}: {
  title: string;
  text: string;
  submit: string;
  pending: boolean;
  error: string | null;
  member: Member;
  members: Member[];
  tasks: boolean;
  onCancel: () => void;
  onSubmit: (conversationsTo: string | null, tasksTo: string | null) => void;
}) {
  const { t } = useTranslation();
  const titleId = useId();
  const textId = useId();
  const convsId = useId();
  const tasksId = useId();
  const first = useRef<HTMLSelectElement>(null);
  const [convsTo, setConvsTo] = useState("");
  const [tasksTo, setTasksTo] = useState("");
  useEffect(() => first.current?.focus(), []);
  const others = members.filter((m) => m.active && m.user_id !== member.user_id);
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
          onSubmit(convsTo || null, tasksTo || null);
        }}
      >
        <h2 id={titleId} className="m-0 text-[15.5px] font-semibold">
          {title}
        </h2>
        <p id={textId} className="m-0 text-[13px] text-ink-2">
          {text}
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
        {tasks ? (
          <>
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
          </>
        ) : null}
        <FormMessage tone="error" message={error} />
        <div className="flex justify-end gap-2">
          <Button type="button" size="sm" onClick={onCancel}>
            {t("settings.team.removeDialog.cancel")}
          </Button>
          <Button type="submit" size="sm" variant={tasks ? "destructive" : "primary"} disabled={pending}>
            {submit}
          </Button>
        </div>
      </form>
    </div>
  );
}
