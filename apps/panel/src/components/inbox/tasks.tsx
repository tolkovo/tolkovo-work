import type { Conversation, MessageEvent } from "@tolkovo/api-client";
import { useCan, useConversationTasks, useLinkTaskConversation, useTaskDraft, useTasks } from "@tolkovo/api-client/react";
import { Link } from "@tanstack/react-router";
import { Button, Skeleton } from "@tolkovo/ui";
import { ListTodo } from "lucide-react";
import { useEffect, useId, useRef, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

import { describeApiError } from "../../lib/api-errors";
import { FormMessage, TextField } from "../form/fields";
import type { MenuItem } from "../menu";
import { TaskStatusBadge } from "../tasks/task-bits";
import { NewTaskForm } from "../tasks/task-form";

/**
 * Tasks of the tasks section from a conversation (ADR-0015, docs/04 §3). Like a GitHub issue,
 * a task keeps the conversation waiting for the fix; done, it brings it back with a reply.
 * Unlike an issue it stays in Tolkovo, so the customer's words go into it as they are.
 */

export type TaskMode = "create" | "link";

/** Whether the conversation offers tasks: the right, the section on, the button chosen. */
function useTasksOffered(c: Conversation): boolean {
  const canView = useCan("tasks.view");
  const canEdit = useCan("tasks.edit");
  const tasks = useConversationTasks(c.id, { enabled: canView });
  const s = tasks.data?.settings;
  return canEdit && !!s && s.enabled && s.conversation_button !== "github";
}

/** "Create task" and "Link to a task" for the ⋯ menu of the header. */
export function useTaskMenuItems(c: Conversation, onOpen: (mode: TaskMode) => void): MenuItem[] {
  const { t } = useTranslation();
  if (!useTasksOffered(c)) return [];
  return [
    { label: t("inbox.tasks.create"), onSelect: () => onOpen("create") },
    { label: t("inbox.tasks.link"), onSelect: () => onOpen("link") },
  ];
}

type TaskFormProps = {
  conversation: Conversation;
  mode: TaskMode;
  onMode: (mode: TaskMode) => void;
  onClose: () => void;
  onDone: (message: string) => void;
};

/** The task form in place of the feed: a new task from the draft, or a link to an open one. */
export function ConversationTaskForm({ conversation: c, mode, onMode, onClose, onDone }: TaskFormProps) {
  const { t } = useTranslation();
  const heading = useRef<HTMLHeadingElement>(null);
  useEffect(() => heading.current?.focus(), [mode]);
  return (
    <section
      aria-labelledby="task-form-title"
      onKeyDown={(e) => {
        if (e.key === "Escape") {
          e.stopPropagation();
          onClose();
        }
      }}
      className="flex min-h-0 flex-1 flex-col gap-4 overflow-auto bg-paper px-5 py-4 max-nav:px-3"
    >
      <div className="flex flex-wrap items-center gap-3">
        <h3 id="task-form-title" ref={heading} tabIndex={-1} className="m-0 mr-auto flex items-center gap-2 text-[15px] font-semibold outline-none">
          <ListTodo aria-hidden="true" className="size-4 text-ink-2" />
          {t(mode === "create" ? "inbox.tasks.createTitle" : "inbox.tasks.linkTitle")}
        </h3>
        <div role="group" aria-label={t("inbox.issues.mode")} className="flex rounded-lg border border-line bg-surface p-0.5">
          {(["create", "link"] as const).map((m) => (
            <button
              key={m}
              type="button"
              aria-pressed={mode === m}
              onClick={() => onMode(m)}
              className="rounded-md px-2.5 py-1 text-[13px] text-ink-2 hover:text-ink aria-pressed:bg-sel aria-pressed:font-medium aria-pressed:text-ink"
            >
              {t(m === "create" ? "inbox.issues.modeCreate" : "inbox.issues.modeLink")}
            </button>
          ))}
        </div>
      </div>
      {mode === "create" ? (
        <CreateTask conversationId={c.id} onClose={onClose} onDone={onDone} />
      ) : (
        <LinkTask conversationId={c.id} onClose={onClose} onDone={onDone} />
      )}
    </section>
  );
}

function CreateTask({ conversationId, onClose, onDone }: { conversationId: string; onClose: () => void; onDone: (m: string) => void }) {
  const { t } = useTranslation();
  const draft = useTaskDraft(conversationId);
  if (draft.isPending) {
    return (
      <div aria-busy="true" aria-label={t("app.loading")} className="flex flex-col gap-3">
        <p className="m-0 text-[13px] text-ink-3">{t("inbox.issues.drafting")}</p>
        <Skeleton className="h-9" />
        <Skeleton className="h-48" />
      </div>
    );
  }
  if (draft.isError) {
    return (
      <div className="flex flex-col items-start gap-3">
        <FormMessage tone="error" message={describeApiError(t, draft.error).message} />
        <Button size="sm" onClick={onClose}>
          {t("tasks.cancel")}
        </Button>
      </div>
    );
  }
  return (
    <div className="flex flex-col gap-3">
      <p className="m-0 rounded-lg border border-note-line bg-note px-3 py-2 text-[13px] text-ink">{t("inbox.tasks.dataNote")}</p>
      {draft.data.drafted_by === "ai" && (
        <p role="note" className="m-0 text-[13px] text-ink-2">
          {t("inbox.tasks.draftedByAi")}
        </p>
      )}
      {draft.data.ai_fallback && (
        <p role="note" className="m-0 text-[13px] text-ink-2">
          {t(`inbox.tasks.aiFallback.${draft.data.ai_fallback}`)}
        </p>
      )}
      <NewTaskForm
        initial={{ title: draft.data.title, body: draft.data.body }}
        conversationId={conversationId}
        onCancel={onClose}
        onDone={(task) => onDone(t("inbox.tasks.created", { number: task.number }))}
      />
    </div>
  );
}

function LinkTask({ conversationId, onClose, onDone }: { conversationId: string; onClose: () => void; onDone: (m: string) => void }) {
  const { t } = useTranslation();
  const [query, setQuery] = useState("");
  const [debounced, setDebounced] = useState("");
  useEffect(() => {
    const timer = setTimeout(() => setDebounced(query.trim()), 300);
    return () => clearTimeout(timer);
  }, [query]);
  const open = useTasks({ state: "open", ...(debounced ? { q: debounced } : {}) });
  const link = useLinkTaskConversation();
  const [picked, setPicked] = useState<string | null>(null);
  const [error, setError] = useState<string | undefined>();
  const listId = useId();
  const items = (open.data?.pages.flatMap((p) => p.items) ?? []).slice(0, 20);

  const submit = (e: FormEvent) => {
    e.preventDefault();
    const task = items.find((i) => i.id === picked);
    if (!task) {
      setError(t("inbox.tasks.pickRequired"));
      return;
    }
    setError(undefined);
    link.mutate({ taskId: task.id, conversationId }, { onSuccess: () => onDone(t("inbox.tasks.linked", { number: task.number })) });
  };

  return (
    <form noValidate onSubmit={submit} className="flex max-w-[640px] flex-col gap-4">
      <TextField
        id="task-search"
        name="q"
        label={t("inbox.tasks.search")}
        hint={t("inbox.tasks.searchHint")}
        value={query}
        autoComplete="off"
        error={error}
        onChange={(e) => setQuery(e.currentTarget.value)}
      />
      {open.isPending ? (
        <Skeleton className="h-24" />
      ) : items.length === 0 ? (
        <p className="m-0 text-[13px] text-ink-3">{t("inbox.tasks.noOpen")}</p>
      ) : (
        <fieldset className="m-0 flex flex-col gap-1 border-0 p-0">
          <legend id={listId} className="mb-1.5 p-0 text-[13px] font-medium">
            {t("inbox.tasks.openTasks")}
          </legend>
          {items.map((i) => (
            <label key={i.id} className="flex min-w-0 items-center gap-2 rounded-md px-2 py-1.5 text-[13px] hover:bg-hover">
              <input type="radio" name="open-task" checked={picked === i.id} onChange={() => setPicked(i.id)} />
              <span className="shrink-0 font-mono text-ink-2">{t("tasks.number", { number: i.number })}</span>
              <span className="truncate">{i.title}</span>
            </label>
          ))}
        </fieldset>
      )}
      <FormMessage tone="error" message={link.isError ? describeApiError(t, link.error).message : null} />
      <div className="flex flex-wrap gap-2">
        <Button type="submit" variant="primary" disabled={link.isPending}>
          {t("inbox.tasks.linkSubmit")}
        </Button>
        <Button type="button" onClick={onClose}>
          {t("tasks.cancel")}
        </Button>
      </div>
    </form>
  );
}

/** The tasks of the conversation in the customer card: number, title and status, linked to the task. */
export function ConversationTasksList({ conversation: c }: { conversation: Conversation }) {
  const { t } = useTranslation();
  const tasks = useConversationTasks(c.id);
  if (tasks.isPending) return <Skeleton className="h-10" />;
  if (tasks.isError) {
    return (
      <p role="alert" className="m-0 text-[13px] text-ink-2">
        {t("inbox.tasks.loadError")}
      </p>
    );
  }
  if (tasks.data.items.length === 0) return <p className="m-0 text-[13px] text-ink-3">{t("inbox.tasks.none")}</p>;
  return (
    <ul className="m-0 flex list-none flex-col gap-2.5 p-0">
      {tasks.data.items.map((task) => (
        <li key={task.id} className="flex min-w-0 items-start gap-2 text-[13px]">
          <TaskStatusBadge status={task.status} />
          <Link to="/tasks/$taskId" params={{ taskId: task.id }} className="min-w-0 break-words text-ink">
            <span className="font-mono text-ink-2">{t("tasks.number", { number: task.number })}</span> {task.title}
          </Link>
        </li>
      ))}
    </ul>
  );
}

/** Whether the card shows the tasks section: the right and the section on. */
export function useTasksInCard(c: Conversation): boolean {
  const canView = useCan("tasks.view");
  const tasks = useConversationTasks(c.id, { enabled: canView });
  return canView && (tasks.data?.settings.enabled !== false || (tasks.data?.items.length ?? 0) > 0);
}

const EVENT_LABELS = {
  task_created: "inbox.event.taskCreated",
  task_linked: "inbox.event.taskLinked",
  task_unlinked: "inbox.event.taskUnlinked",
  task_reopened: "inbox.event.taskReopened",
  task_closed: "inbox.event.taskDone",
} as const;

/** The feed line of a task event, linked to the task, with what happened to the conversation. */
export function TaskEventText({ event: e, by }: { event: MessageEvent; by: string }) {
  const { t } = useTranslation();
  const type = e.type as keyof typeof EVENT_LABELS;
  const label = type === "task_closed" && e.task?.reason === "canceled" ? "inbox.event.taskCanceled" : (EVENT_LABELS[type] ?? "inbox.event.taskLinked");
  if (!e.task) return <>{t(label, { number: "" }) + by}</>;
  const [before, after] = t(label, { number: "\u0000" }).split("\u0000");
  return (
    <>
      {before}
      <Link to="/tasks/$taskId" params={{ taskId: e.task.task_id }} className="text-ink-2 underline">
        {t("tasks.number", { number: e.task.number })}
      </Link>
      {after}
      {e.from !== e.to && e.to === "pending" ? t("inbox.event.awaitingFix") : ""}
      {e.from !== e.to && e.to === "assigned" ? t("inbox.event.backToAssignee") : ""}
      {e.from !== e.to && e.to === "queued" ? t("inbox.event.backToQueue") : ""}
      {by}
    </>
  );
}

/**
 * The reply about a done task: the composer offers the section's reply in the customer's
 * language. The operator inserts it, edits it and sends it as any reply.
 */
export function TaskReplyOffer({ conversation: c, text, onInsert }: { conversation: Conversation; text: string; onInsert: (t: string) => void }) {
  const { t } = useTranslation();
  const canView = useCan("tasks.view");
  const tasks = useConversationTasks(c.id, { enabled: canView });
  const d = tasks.data?.reply_draft;
  if (!d || text === d.text) return null;
  return (
    <div className="mb-2 flex flex-wrap items-center gap-2 rounded-md border border-teal bg-teal-soft px-3 py-2 text-[13px] text-ink">
      <ListTodo aria-hidden="true" className="size-4 shrink-0 text-teal" />
      <span className="mr-auto min-w-0">
        {t("inbox.tasks.done", { number: d.number })} <span className="text-ink-2">«{d.text}»</span>
      </span>
      <Button size="sm" variant="primary" onClick={() => onInsert(d.text)}>
        {t("inbox.issues.insertReply")}
      </Button>
    </div>
  );
}
