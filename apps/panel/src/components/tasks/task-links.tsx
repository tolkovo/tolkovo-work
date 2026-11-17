import { isApiError, type Task, type TaskLink, type TaskLinkKind } from "@tolkovo/api-client";
import { useAddTaskLink, useCan, useCloseTaskAsDuplicate, useRemoveTaskLink, useTaskLinks } from "@tolkovo/api-client/react";
import { Link } from "@tanstack/react-router";
import { Button, Label, Select, Skeleton, cn } from "@tolkovo/ui";
import { Copy, Link2, Plus, X } from "lucide-react";
import { useEffect, useId, useRef, useState, type FormEvent } from "react";
import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";

import { describeApiError } from "../../lib/api-errors";
import { taskRef } from "../../lib/tasks/tasks";
import { FormMessage } from "../form/fields";
import { TaskStatusBadge, control } from "./task-bits";

/** The kinds a member adds by hand; a duplicate is closed by its own action. */
const ADD_KINDS: TaskLinkKind[] = ["waits_for", "blocks", "relates"];

/** An example ref for the other task: a neighbour of this one, not the task itself. */
function exampleRef(task: Task): string {
  return `${task.project_key}-${task.number > 1 ? task.number - 1 : task.number + 1}`;
}

/** The words of a failed link: a task not found is told as such, not as a missing workspace. */
function linkError(t: TFunction, error: unknown): string {
  if (isApiError(error) && error.code === "not_found") return t("tasks.links.notFound");
  return describeApiError(t, error).message;
}

/**
 * Links of the task to other tasks (ADR-0022): what it waits for, what it blocks, related tasks
 * and duplicates. A task in a project the member is not in shows by its status only.
 */
export function TaskLinks({ task }: { task: Task }) {
  const { t } = useTranslation();
  const links = useTaskLinks(task.id);
  const canEdit = useCan("tasks.edit");
  const [adding, setAdding] = useState(false);
  const addButton = useRef<HTMLButtonElement>(null);
  const items = links.data ?? [];
  if (!canEdit && items.length === 0) return null;
  return (
    <section aria-labelledby="task-links" className="flex flex-col gap-2">
      <div className="flex items-center gap-2">
        <h3 id="task-links" className="m-0 mr-auto text-[13px] font-semibold">
          {t("tasks.links.title")}
        </h3>
        {canEdit && !adding && (
          <Button ref={addButton} size="sm" variant="ghost" onClick={() => setAdding(true)}>
            <Plus aria-hidden="true" className="size-4" />
            {t("tasks.links.add")}
          </Button>
        )}
      </div>
      {links.isPending ? (
        <Skeleton className="h-9" />
      ) : links.isError ? (
        <p role="alert" className="m-0 text-[13px] text-ink-2">
          {t("tasks.links.loadError")}
        </p>
      ) : (
        items.length > 0 && (
          <ul className="m-0 flex list-none flex-col gap-1.5 p-0">
            {items.map((l) => (
              <LinkRow key={l.task_id} taskId={task.id} link={l} canEdit={canEdit} />
            ))}
          </ul>
        )
      )}
      {adding && (
        <AddLink
          task={task}
          onClose={() => {
            setAdding(false);
            requestAnimationFrame(() => addButton.current?.focus());
          }}
        />
      )}
    </section>
  );
}

function LinkRow({ taskId, link, canEdit }: { taskId: string; link: TaskLink; canEdit: boolean }) {
  const { t } = useTranslation();
  const remove = useRemoveTaskLink();
  const name = link.visible ? `${link.ref ?? ""} ${link.title ?? ""}`.trim() : t("tasks.links.hidden");
  return (
    <li className="flex min-w-0 flex-col gap-1 rounded-lg border border-line bg-surface px-3 py-2 text-[13px]">
      <span className="flex min-w-0 items-center gap-2">
        <Link2 aria-hidden="true" className="size-4 shrink-0 text-ink-3" />
        <span className={cn("shrink-0 text-[12px] font-medium", link.kind === "waits_for" ? "text-lamp-ink" : "text-ink-2")}>
          {t(`tasks.links.kind.${link.kind}`)}
        </span>
        {link.visible ? (
          <Link to="/tasks/$taskId" params={{ taskId: link.task_id }} search={(prev) => prev} className="mr-auto min-w-0 truncate text-ink">
            <span className="mr-1.5 font-mono text-[12px] text-ink-3">{link.ref}</span>
            {link.title}
          </Link>
        ) : (
          <span className="mr-auto min-w-0 truncate text-ink-3">{t("tasks.links.hidden")}</span>
        )}
        <TaskStatusBadge status={link.status} />
        {canEdit && (
          <button
            type="button"
            aria-label={t("tasks.links.remove", { name })}
            title={t("tasks.links.removeTitle")}
            disabled={remove.isPending}
            onClick={() => remove.mutate({ taskId, otherId: link.task_id })}
            className="grid size-7 shrink-0 place-items-center rounded-md text-ink-3 hover:bg-hover hover:text-ink"
          >
            <X aria-hidden="true" className="size-4" />
          </button>
        )}
      </span>
      {remove.isError && (
        <p role="alert" className="m-0 text-[12.5px] text-danger">
          {linkError(t, remove.error)}
        </p>
      )}
    </li>
  );
}

/** The kind and the other task, by its ref: CRM-41. */
function AddLink({ task, onClose }: { task: Task; onClose: () => void }) {
  const { t } = useTranslation();
  const add = useAddTaskLink();
  const [kind, setKind] = useState<TaskLinkKind>("waits_for");
  const [ref, setRef] = useState("");
  const ids = { kind: useId(), ref: useId(), hint: useId() };
  const field = useRef<HTMLInputElement>(null);
  useEffect(() => field.current?.focus(), []);
  const other = ref.trim();
  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (!other) return;
    add.mutate({ taskId: task.id, kind, task: other }, { onSuccess: onClose });
  };
  return (
    <form
      noValidate
      onSubmit={submit}
      onKeyDown={(e) => {
        if (e.key === "Escape") {
          e.preventDefault();
          onClose();
        }
      }}
      aria-label={t("tasks.links.add")}
      className="flex flex-col gap-2 rounded-lg border border-line bg-surface p-3"
    >
      <div className="flex flex-wrap gap-2">
        <div className="flex min-w-36 flex-col gap-1.5">
          <Label htmlFor={ids.kind}>{t("tasks.links.kindLabel")}</Label>
          <Select id={ids.kind} value={kind} onChange={(e) => setKind(e.currentTarget.value as TaskLinkKind)}>
            {ADD_KINDS.map((k) => (
              <option key={k} value={k}>
                {t(`tasks.links.kind.${k}`)}
              </option>
            ))}
          </Select>
        </div>
        <div className="flex min-w-0 flex-1 basis-40 flex-col gap-1.5">
          <Label htmlFor={ids.ref}>{t("tasks.links.task")}</Label>
          <input
            id={ids.ref}
            ref={field}
            value={ref}
            maxLength={64}
            autoComplete="off"
            spellCheck={false}
            placeholder={t("tasks.links.taskPlaceholder", { ref: exampleRef(task) })}
            aria-describedby={ids.hint}
            aria-invalid={add.isError || undefined}
            onChange={(e) => setRef(e.currentTarget.value)}
            className={cn(control, "h-9 w-full font-mono")}
          />
        </div>
      </div>
      <p id={ids.hint} className="m-0 text-[12.5px] text-ink-3">
        {t(`tasks.links.hint.${kind as "waits_for" | "blocks" | "relates"}`)}
      </p>
      <FormMessage tone="error" message={add.isError ? linkError(t, add.error) : null} />
      <div className="flex gap-2">
        <Button type="submit" size="sm" variant="primary" disabled={!other || add.isPending}>
          {t("tasks.links.submit")}
        </Button>
        <Button type="button" size="sm" onClick={onClose}>
          {t("tasks.cancel")}
        </Button>
      </div>
    </form>
  );
}

/**
 * «Закрыть как дубликат…»: asks for the original, says what happens to the conversations, then
 * cancels the task and links it to the original.
 */
export function DuplicateButton({ task, disabled }: { task: Task; disabled: boolean }) {
  const { t } = useTranslation();
  const canEdit = useCan("tasks.edit");
  const [asking, setAsking] = useState(false);
  const button = useRef<HTMLButtonElement>(null);
  if (!canEdit) return null;
  return (
    <>
      <Button ref={button} variant="ghost" size="sm" className="self-start" disabled={disabled} onClick={() => setAsking(true)}>
        <Copy aria-hidden="true" className="size-4" />
        {t("tasks.duplicate.action")}
      </Button>
      {asking && (
        <DuplicateDialog
          task={task}
          onClose={() => {
            setAsking(false);
            requestAnimationFrame(() => button.current?.focus());
          }}
        />
      )}
    </>
  );
}

function DuplicateDialog({ task, onClose }: { task: Task; onClose: () => void }) {
  const { t } = useTranslation();
  const close = useCloseTaskAsDuplicate();
  const [original, setOriginal] = useState("");
  const ids = { title: useId(), field: useId(), note: useId() };
  const field = useRef<HTMLInputElement>(null);
  useEffect(() => field.current?.focus(), []);
  const ref = original.trim();
  const conversations = task.conversation_ids.length;
  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-labelledby={ids.title}
      className="fixed inset-0 z-50 grid place-items-center bg-ink/35 p-4"
      onKeyDown={(e) => {
        if (e.key === "Escape") {
          e.preventDefault();
          onClose();
        }
      }}
      onClick={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <form
        noValidate
        className="flex w-full max-w-105 flex-col gap-3 rounded-2xl border border-line bg-surface p-4.5 text-ink shadow-pop"
        onSubmit={(e) => {
          e.preventDefault();
          if (ref) close.mutate({ taskId: task.id, original: ref, version: task.version }, { onSuccess: onClose });
        }}
      >
        <h2 id={ids.title} className="m-0 text-[15.5px] font-semibold">
          {t("tasks.duplicate.title", { number: taskRef(task) })}
        </h2>
        <label htmlFor={ids.field} className="text-[13px] font-medium">
          {t("tasks.duplicate.original")}
        </label>
        <input
          id={ids.field}
          ref={field}
          value={original}
          maxLength={64}
          autoComplete="off"
          spellCheck={false}
          placeholder={t("tasks.links.taskPlaceholder", { ref: exampleRef(task) })}
          aria-describedby={ids.note}
          aria-invalid={close.isError || undefined}
          onChange={(e) => setOriginal(e.currentTarget.value)}
          className={cn(control, "h-9 w-full font-mono")}
        />
        <p id={ids.note} className="m-0 text-[12.5px] text-ink-2">
          {t("tasks.duplicate.note")}
          {conversations > 0 && <> {t("tasks.duplicate.conversations", { count: conversations })}</>}
        </p>
        <FormMessage tone="error" message={close.isError ? linkError(t, close.error) : null} />
        <div className="flex justify-end gap-2">
          <Button type="button" size="sm" onClick={onClose}>
            {t("tasks.cancel")}
          </Button>
          <Button type="submit" size="sm" variant="primary" disabled={!ref || close.isPending}>
            {t("tasks.duplicate.submit")}
          </Button>
        </div>
      </form>
    </div>
  );
}
