import type { Line } from "@tolkovo/api-client";
import { useLineMutations, useLines, useMembers } from "@tolkovo/api-client/react";
import { Button, Skeleton } from "@tolkovo/ui";
import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

import { describeApiError } from "../../lib/api-errors";
import { answersCustomers } from "../../lib/team";
import { FormMessage, SelectField, TextField } from "../form/fields";

const LEVELS = [1, 2, 3] as const;
/** At most this many lines (LineList in api/openapi.yaml). */
const MAX_LINES = 30;

/**
 * «Линии» (ADR-0021): groups of operators with a level. New conversations and those from the AI
 * bot are on the first line; an operator passes one up to a line with a reason, and the router
 * gives it to that line's members only.
 */
export function LineSettings() {
  const { t } = useTranslation();
  const lines = useLines();
  const [adding, setAdding] = useState(false);
  const list = lines.data ?? [];
  return (
    <section aria-labelledby="settings-lines-title" aria-busy={lines.isPending} className="flex flex-col gap-3">
      <div className="flex items-center gap-2">
        <h3 id="settings-lines-title" className="m-0 mr-auto text-[15px] font-semibold">
          {t("settings.lines.title")}
        </h3>
        {!adding && lines.isSuccess && list.length < MAX_LINES && (
          <Button size="sm" onClick={() => setAdding(true)}>
            {t("settings.lines.add")}
          </Button>
        )}
      </div>
      <p className="m-0 text-[13px] text-ink-2">{t("settings.lines.lead")}</p>
      {adding && <LineForm onDone={() => setAdding(false)} />}
      {lines.isPending ? (
        <div role="status">
          <span className="sr-only">{t("app.loading")}</span>
          <Skeleton className="h-16" />
        </div>
      ) : lines.isError ? (
        <div role="alert" className="flex flex-col items-start gap-2">
          <p className="m-0 text-ink-2">{describeApiError(t, lines.error).message}</p>
          <Button onClick={() => void lines.refetch()}>{t("error.retry")}</Button>
        </div>
      ) : list.length === 0 && !adding ? (
        <p className="m-0 text-[13px] text-ink-3">{t("settings.lines.empty")}</p>
      ) : (
        <ul className="m-0 flex list-none flex-col gap-2 p-0">
          {list.map((line) => (
            <LineItem key={line.id} line={line} />
          ))}
        </ul>
      )}
    </section>
  );
}

function LineItem({ line }: { line: Line }) {
  const { t } = useTranslation();
  const { data: members } = useMembers();
  const { remove } = useLineMutations();
  const [editing, setEditing] = useState(false);
  const [confirming, setConfirming] = useState(false);
  if (editing) {
    return (
      <li>
        <LineForm line={line} onDone={() => setEditing(false)} />
      </li>
    );
  }
  const names = line.member_ids.map((id) => members?.find((m) => m.user_id === id)?.name).filter(Boolean);
  return (
    <li className="flex flex-wrap items-center gap-2 rounded-lg border border-line bg-surface px-3 py-2 text-[13px]">
      <span className="rounded-sm bg-sunk px-1.5 py-0.5 font-mono text-[11px] font-medium">{t("inbox.line.level", { level: line.level })}</span>
      <span className="flex min-w-0 flex-1 flex-col gap-0.5">
        <span className="font-medium">{line.name}</span>
        <span className="text-[12.5px] text-ink-2">{names.length > 0 ? names.join(", ") : t("settings.lines.nobody")}</span>
      </span>
      {confirming ? (
        <span className="flex items-center gap-2">
          <span className="text-ink-2">{t("settings.lines.deleteConfirm")}</span>
          <Button size="sm" variant="destructive" disabled={remove.isPending} onClick={() => remove.mutate(line.id)}>
            {t("settings.lines.deleteSubmit")}
          </Button>
          <Button size="sm" onClick={() => setConfirming(false)}>
            {t("tasks.cancel")}
          </Button>
        </span>
      ) : (
        <>
          <Button size="sm" variant="ghost" onClick={() => setEditing(true)}>
            {t("settings.lines.edit")}
          </Button>
          <Button size="sm" variant="ghost" className="text-danger" onClick={() => setConfirming(true)}>
            {t("settings.lines.delete")}
          </Button>
        </>
      )}
      {remove.isError && (
        <p role="alert" className="m-0 w-full text-danger">
          {describeApiError(t, remove.error).message}
        </p>
      )}
    </li>
  );
}

function LineForm({ line, onDone }: { line?: Line; onDone: () => void }) {
  const { t } = useTranslation();
  const { data: members } = useMembers();
  const { create, update } = useLineMutations();
  const save = line ? update : create;
  const [name, setName] = useState(line?.name ?? "");
  const [level, setLevel] = useState(line?.level ?? 2);
  const [memberIds, setMemberIds] = useState<string[]>(line?.member_ids ?? []);
  const [nameError, setNameError] = useState<string | undefined>();
  const key = line?.id ?? "new";

  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (!name.trim()) {
      setNameError(t("settings.lines.nameRequired"));
      document.getElementById(`line-name-${key}`)?.focus();
      return;
    }
    setNameError(undefined);
    const body = { name: name.trim(), level, member_ids: memberIds };
    if (line) update.mutate({ id: line.id, body }, { onSuccess: onDone });
    else create.mutate(body, { onSuccess: onDone });
  };

  const error = save.isError ? describeApiError(t, save.error, { line_exists: "name" }) : null;
  return (
    <form noValidate onSubmit={submit} className="flex flex-col gap-3 rounded-lg border border-teal bg-surface p-3">
      <TextField
        id={`line-name-${key}`}
        name="name"
        label={t("settings.lines.name")}
        hint={t("settings.lines.nameHint")}
        value={name}
        maxLength={60}
        error={nameError ?? (error?.field === "name" ? error.message : undefined)}
        onChange={(e) => setName(e.currentTarget.value)}
      />
      <SelectField
        id={`line-level-${key}`}
        name="level"
        label={t("settings.lines.level")}
        hint={t("settings.lines.levelHint")}
        value={String(level)}
        onChange={(e) => setLevel(Number(e.currentTarget.value))}
      >
        {LEVELS.map((l) => (
          <option key={l} value={l}>
            {t(`settings.lines.levels.${l}`)}
          </option>
        ))}
      </SelectField>
      <fieldset className="m-0 flex flex-col gap-1 border-0 p-0">
        <legend className="mb-1 p-0 text-[13px] font-medium">{t("settings.lines.members")}</legend>
        <p className="m-0 mb-1 text-[12.5px] text-ink-2">{t("settings.lines.membersHint")}</p>
        {members
          ?.filter((m) => m.active && answersCustomers(m.role))
          .map((m) => (
            <label key={m.user_id} className="flex items-center gap-2 text-[13px]">
              <input
                type="checkbox"
                checked={memberIds.includes(m.user_id)}
                onChange={(e) => {
                  const on = e.currentTarget.checked;
                  setMemberIds((ids) => (on ? [...ids, m.user_id] : ids.filter((id) => id !== m.user_id)));
                }}
                className="size-4 accent-teal"
              />
              {m.name}
            </label>
          ))}
      </fieldset>
      <FormMessage tone="error" message={error && error.field !== "name" ? error.message : null} />
      <div className="flex gap-2">
        <Button type="submit" size="sm" variant="primary" disabled={save.isPending}>
          {t("settings.lines.save")}
        </Button>
        <Button type="button" size="sm" onClick={onDone}>
          {t("tasks.cancel")}
        </Button>
      </div>
    </form>
  );
}
