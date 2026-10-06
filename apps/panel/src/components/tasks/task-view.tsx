import type { Task, TaskActivity, TaskUpdate } from "@tolkovo/api-client";
import {
  useCan,
  useCommentTask,
  useConversation,
  useDeleteTask,
  useMembers,
  useSession,
  useTask,
  useTaskActivity,
  useTaskQueues,
  useUnlinkTaskConversation,
  useUpdateTask,
} from "@tolkovo/api-client/react";
import { Link, useNavigate } from "@tanstack/react-router";
import { Button, Label, Select, Skeleton, cn } from "@tolkovo/ui";
import { ArrowLeft, MessagesSquare, X } from "lucide-react";
import { useEffect, useId, useRef, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

import { describeApiError } from "../../lib/api-errors";
import { TASK_PRIORITIES, TASK_STATUSES, isClosed, taskUpdate, type TasksSearch } from "../../lib/tasks/tasks";
import { FormMessage, TextField } from "../form/fields";
import { useMemberNames } from "../inbox/presence";
import { TaskPriorityMark, TaskStatusBadge, control } from "./task-bits";

/** The open task (ADR-0015): its fields, description, conversations and feed. */
export function TaskView({ taskId, search }: { taskId: string; search: TasksSearch }) {
  const { t } = useTranslation();
  const task = useTask(taskId);
  if (task.isPending) {
    return (
      <div className="flex flex-1 flex-col gap-3 p-5" aria-busy="true" aria-label={t("app.loading")}>
        <Skeleton className="h-8 w-2/3" />
        <Skeleton className="h-40" />
      </div>
    );
  }
  if (task.isError) {
    return (
      <div role="alert" className="m-auto flex max-w-[360px] flex-col items-center gap-3 p-6 text-center">
        <p className="m-0 text-ink-2">{describeApiError(t, task.error).message}</p>
        <Button asChild size="sm">
          <Link to="/tasks" search={search}>
            {t("tasks.backToList")}
          </Link>
        </Button>
      </div>
    );
  }
  return <Loaded key={task.data.id} task={task.data} search={search} />;
}

function Loaded({ task, search }: { task: Task; search: TasksSearch }) {
  const { t } = useTranslation();
  const update = useUpdateTask(task.id);
  const heading = useRef<HTMLHeadingElement>(null);
  useEffect(() => heading.current?.focus(), []);
  const [editing, setEditing] = useState(false);
  const change = (patch: Partial<Omit<TaskUpdate, "version">>) => update.mutate(taskUpdate(task, patch));
  return (
    <article aria-labelledby="task-title" className="flex min-h-0 min-w-0 flex-1 flex-col overflow-auto bg-paper">
      <header className="flex flex-col gap-2 border-b border-line bg-surface px-5 py-3 max-nav:px-3">
        <Link to="/tasks" search={search} className="hidden items-center gap-1 text-[13px] text-ink-2 no-underline max-nav:flex">
          <ArrowLeft aria-hidden="true" className="size-4" />
          {t("tasks.backToList")}
        </Link>
        <div className="flex min-w-0 items-start gap-3">
          <h2 id="task-title" ref={heading} tabIndex={-1} className="m-0 mr-auto min-w-0 text-[17px] leading-snug font-semibold break-words outline-none">
            <span className="mr-2 font-mono text-[14px] font-normal text-ink-3">{t("tasks.number", { number: task.number })}</span>
            {task.title}
          </h2>
          {!editing && (
            <Button size="sm" onClick={() => setEditing(true)}>
              {t("tasks.edit")}
            </Button>
          )}
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <TaskStatusBadge status={task.status} />
          <TaskPriorityMark priority={task.priority} quietNormal={false} />
        </div>
      </header>
      {update.isError && (
        <p role="alert" className="m-0 border-b border-line bg-danger-soft px-5 py-2 text-[13px] text-ink">
          {describeApiError(t, update.error).message}
        </p>
      )}
      <div className="grid grid-cols-[minmax(0,1fr)_260px] gap-6 px-5 py-4 max-wide:grid-cols-[minmax(0,1fr)] max-nav:px-3">
        <div className="flex min-w-0 flex-col gap-5">
          {editing ? (
            <EditForm task={task} onClose={() => setEditing(false)} />
          ) : (
            <section aria-label={t("tasks.fields.body")}>
              {task.body ? (
                <p className="m-0 text-[14px] leading-relaxed break-words whitespace-pre-wrap">{task.body}</p>
              ) : (
                <p className="m-0 text-[13px] text-ink-3">{t("tasks.noBody")}</p>
              )}
            </section>
          )}
          <Conversations task={task} />
          <Feed task={task} />
        </div>
        <Fields task={task} disabled={update.isPending} onChange={change} />
      </div>
    </article>
  );
}

/** Status, priority, assignee and queue: each change is saved at once. */
function Fields({ task, disabled, onChange }: { task: Task; disabled: boolean; onChange: (patch: Partial<Omit<TaskUpdate, "version">>) => void }) {
  const { t } = useTranslation();
  const { data: session } = useSession();
  const { data: members } = useMembers();
  const { data: queues } = useTaskQueues();
  const canManage = useCan("tasks.manage");
  const ids = { status: useId(), priority: useId(), assignee: useId(), queue: useId() };
  const me = session?.user.id;
  return (
    <aside aria-label={t("tasks.fieldsLabel")} className="flex flex-col gap-3">
      {me && task.assignee_id !== me && !isClosed(task.status) && (
        <Button variant="primary" disabled={disabled} onClick={() => onChange({ assignee_id: me, status: "in_progress" })}>
          {t("tasks.take")}
        </Button>
      )}
      <div className="flex flex-col gap-1.5">
        <Label htmlFor={ids.status}>{t("tasks.fields.status")}</Label>
        <Select id={ids.status} value={task.status} disabled={disabled} onChange={(e) => onChange({ status: e.currentTarget.value as Task["status"] })}>
          {TASK_STATUSES.map((s) => (
            <option key={s} value={s}>
              {t(`tasks.status.${s}`)}
            </option>
          ))}
        </Select>
      </div>
      <div className="flex flex-col gap-1.5">
        <Label htmlFor={ids.priority}>{t("tasks.fields.priority")}</Label>
        <Select
          id={ids.priority}
          value={task.priority}
          disabled={disabled}
          onChange={(e) => onChange({ priority: e.currentTarget.value as Task["priority"] })}
        >
          {TASK_PRIORITIES.map((p) => (
            <option key={p} value={p}>
              {t(`tasks.priority.${p}`)}
            </option>
          ))}
        </Select>
      </div>
      <div className="flex flex-col gap-1.5">
        <Label htmlFor={ids.assignee}>{t("tasks.fields.assignee")}</Label>
        <Select id={ids.assignee} value={task.assignee_id ?? ""} disabled={disabled} onChange={(e) => onChange({ assignee_id: e.currentTarget.value || null })}>
          <option value="">{t("tasks.unassigned")}</option>
          {members?.map((m) => (
            <option key={m.user_id} value={m.user_id}>
              {m.name}
            </option>
          ))}
        </Select>
      </div>
      <div className="flex flex-col gap-1.5">
        <Label htmlFor={ids.queue}>{t("tasks.fields.queue")}</Label>
        <Select id={ids.queue} value={task.queue_id ?? ""} disabled={disabled} onChange={(e) => onChange({ queue_id: e.currentTarget.value || null })}>
          <option value="">{t("tasks.noQueue")}</option>
          {queues?.map((q) => (
            <option key={q.id} value={q.id}>
              {q.name}
            </option>
          ))}
        </Select>
      </div>
      <Dates task={task} />
      {canManage && <DeleteTask task={task} />}
    </aside>
  );
}

function Dates({ task }: { task: Task }) {
  const { t, i18n } = useTranslation();
  const names = useMemberNames();
  const fmt = (iso: string) => new Intl.DateTimeFormat(i18n.language, { dateStyle: "medium", timeStyle: "short" }).format(new Date(iso));
  return (
    <dl className="m-0 grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1 text-[12.5px] text-ink-2">
      <dt className="text-ink-3">{t("tasks.created")}</dt>
      <dd className="m-0">
        {fmt(task.created_at)}
        {task.created_by && <> · {names.get(task.created_by) ?? t("inbox.colleague")}</>}
      </dd>
      {task.closed_at && (
        <>
          <dt className="text-ink-3">{t("tasks.closed")}</dt>
          <dd className="m-0">{fmt(task.closed_at)}</dd>
        </>
      )}
    </dl>
  );
}

function DeleteTask({ task }: { task: Task }) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const remove = useDeleteTask();
  const [confirm, setConfirm] = useState(false);
  if (!confirm) {
    return (
      <Button variant="ghost" size="sm" className="self-start text-danger" onClick={() => setConfirm(true)}>
        {t("tasks.delete")}
      </Button>
    );
  }
  return (
    <div role="group" aria-label={t("tasks.delete")} className="flex flex-col gap-2 rounded-lg border border-line bg-surface p-3 text-[13px]">
      <p className="m-0">{t("tasks.deleteConfirm")}</p>
      <div className="flex gap-2">
        <Button
          size="sm"
          variant="destructive"
          disabled={remove.isPending}
          onClick={() => remove.mutate(task.id, { onSuccess: () => void navigate({ to: "/tasks" }) })}
        >
          {t("tasks.deleteSubmit")}
        </Button>
        <Button size="sm" onClick={() => setConfirm(false)}>
          {t("tasks.cancel")}
        </Button>
      </div>
      {remove.isError && <p className="m-0 text-danger">{describeApiError(t, remove.error).message}</p>}
    </div>
  );
}

function EditForm({ task, onClose }: { task: Task; onClose: () => void }) {
  const { t } = useTranslation();
  const update = useUpdateTask(task.id);
  const [title, setTitle] = useState(task.title);
  const [body, setBody] = useState(task.body);
  const [titleError, setTitleError] = useState<string | undefined>();
  const bodyId = useId();
  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (!title.trim()) {
      setTitleError(t("tasks.titleRequired"));
      document.getElementById("task-edit-title")?.focus();
      return;
    }
    update.mutate(taskUpdate(task, { title: title.trim(), body }), { onSuccess: onClose });
  };
  return (
    <form noValidate onSubmit={submit} onKeyDown={(e) => e.key === "Escape" && onClose()} className="flex flex-col gap-3">
      <TextField
        id="task-edit-title"
        name="title"
        label={t("tasks.fields.title")}
        value={title}
        maxLength={256}
        error={titleError}
        onChange={(e) => setTitle(e.currentTarget.value.replace(/[\r\n]+/g, " "))}
      />
      <div className="flex flex-col gap-1.5">
        <Label htmlFor={bodyId}>{t("tasks.fields.body")}</Label>
        <textarea id={bodyId} value={body} rows={10} maxLength={20000} onChange={(e) => setBody(e.currentTarget.value)} className={cn(control, "py-2 leading-relaxed")} />
      </div>
      <FormMessage tone="error" message={update.isError ? describeApiError(t, update.error).message : null} />
      <div className="flex gap-2">
        <Button type="submit" variant="primary" disabled={update.isPending}>
          {t("tasks.save")}
        </Button>
        <Button type="button" onClick={onClose}>
          {t("tasks.cancel")}
        </Button>
      </div>
    </form>
  );
}

/** Conversations linked to the task, with their number and customer; unlink is at hand. */
function Conversations({ task }: { task: Task }) {
  const { t } = useTranslation();
  if (task.conversation_ids.length === 0) return null;
  return (
    <section aria-labelledby="task-conversations" className="flex flex-col gap-2">
      <h3 id="task-conversations" className="m-0 text-[13px] font-semibold">
        {t("tasks.conversations")}
      </h3>
      <ul className="m-0 flex list-none flex-col gap-1.5 p-0">
        {task.conversation_ids.map((id) => (
          <ConversationLink key={id} taskId={task.id} conversationId={id} />
        ))}
      </ul>
    </section>
  );
}

function ConversationLink({ taskId, conversationId }: { taskId: string; conversationId: string }) {
  const { t } = useTranslation();
  const c = useConversation(conversationId);
  const unlink = useUnlinkTaskConversation();
  return (
    <li className="flex min-w-0 items-center gap-2 rounded-lg border border-line bg-surface px-3 py-2 text-[13px]">
      <MessagesSquare aria-hidden="true" className="size-4 shrink-0 text-ink-3" />
      {c.data ? (
        <Link to="/inbox/$conversationId" params={{ conversationId }} search={{ tab: "all" }} className="mr-auto min-w-0 truncate text-ink">
          {t("inbox.number", { number: c.data.number })} · {c.data.contact.name}
        </Link>
      ) : (
        <span className="mr-auto text-ink-3">{c.isError ? t("tasks.conversationHidden") : t("app.loading")}</span>
      )}
      <button
        type="button"
        aria-label={t("tasks.unlink")}
        title={t("tasks.unlink")}
        disabled={unlink.isPending}
        onClick={() => unlink.mutate({ taskId, conversationId })}
        className="grid size-7 shrink-0 place-items-center rounded-md text-ink-3 hover:bg-hover hover:text-ink"
      >
        <X aria-hidden="true" className="size-4" />
      </button>
    </li>
  );
}

/** Comments and changes, oldest first, with the comment box below. */
function Feed({ task }: { task: Task }) {
  const { t } = useTranslation();
  const activity = useTaskActivity(task.id);
  return (
    <section aria-labelledby="task-feed" className="flex flex-col gap-3">
      <h3 id="task-feed" className="m-0 text-[13px] font-semibold">
        {t("tasks.feed")}
      </h3>
      {activity.isPending ? (
        <Skeleton className="h-16" />
      ) : activity.isError ? (
        <p role="alert" className="m-0 text-[13px] text-ink-2">
          {t("tasks.feedError")}
        </p>
      ) : (
        <ol className="m-0 flex list-none flex-col gap-2 p-0">
          {activity.data.map((a) => (
            <ActivityLine key={a.id} activity={a} />
          ))}
        </ol>
      )}
      <CommentForm taskId={task.id} />
    </section>
  );
}

function ActivityLine({ activity: a }: { activity: TaskActivity }) {
  const { t, i18n } = useTranslation();
  const names = useMemberNames();
  const { data: queues } = useTaskQueues();
  const who = a.actor_id ? (names.get(a.actor_id) ?? t("inbox.colleague")) : t("inbox.sender.system");
  const time = new Intl.DateTimeFormat(i18n.language, { dateStyle: "short", timeStyle: "short" }).format(new Date(a.created_at));
  if (a.kind === "comment") {
    return (
      <li className="flex flex-col gap-1 rounded-lg border border-line bg-surface px-3 py-2">
        <span className="text-[12px] text-ink-3">
          <b className="font-medium text-ink-2">{who}</b> · {time}
        </span>
        <p className="m-0 text-[13.5px] leading-relaxed break-words whitespace-pre-wrap">{a.body}</p>
      </li>
    );
  }
  const member = (id?: string) => (id ? (names.get(id) ?? t("inbox.colleague")) : t("tasks.unassigned"));
  const queue = (id?: string) => (id ? (queues?.find((q) => q.id === id)?.name ?? t("tasks.queueDeleted")) : t("tasks.noQueue"));
  let text: string;
  switch (a.kind) {
    case "created":
      text = t("tasks.activity.created");
      break;
    case "status":
      text = t("tasks.activity.status", { to: t(`tasks.status.${a.to as Task["status"]}`) });
      break;
    case "priority":
      text = t("tasks.activity.priority", { to: t(`tasks.priority.${a.to as Task["priority"]}`) });
      break;
    case "assignee":
      text = a.to ? t("tasks.activity.assignee", { name: member(a.to) }) : t("tasks.activity.unassigned");
      break;
    case "queue":
      text = t("tasks.activity.queue", { name: queue(a.to) });
      break;
    case "edited":
      text = t("tasks.activity.edited");
      break;
    case "linked":
      text = t("tasks.activity.linked");
      break;
    case "unlinked":
      text = t("tasks.activity.unlinked");
      break;
    default:
      text = "";
  }
  return (
    <li className="px-1 text-[12.5px] text-ink-3">
      {text} · {who} · {time}
    </li>
  );
}

function CommentForm({ taskId }: { taskId: string }) {
  const { t } = useTranslation();
  const comment = useCommentTask(taskId);
  const [text, setText] = useState("");
  const id = useId();
  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (!text.trim()) return;
    comment.mutate(text.trim(), { onSuccess: () => setText("") });
  };
  return (
    <form noValidate onSubmit={submit} className="flex flex-col gap-2">
      <Label htmlFor={id} className="sr-only">
        {t("tasks.comment")}
      </Label>
      <textarea
        id={id}
        value={text}
        rows={3}
        maxLength={4000}
        placeholder={t("tasks.commentPlaceholder")}
        onChange={(e) => setText(e.currentTarget.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) submit(e);
        }}
        className={cn(control, "py-2 leading-relaxed")}
      />
      <FormMessage tone="error" message={comment.isError ? describeApiError(t, comment.error).message : null} />
      <Button type="submit" size="sm" className="self-start" disabled={comment.isPending || !text.trim()}>
        {t("tasks.commentSubmit")}
      </Button>
    </form>
  );
}
