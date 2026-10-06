import type { Task, TaskPriority } from "@tolkovo/api-client";
import { useCreateTask, useMembers, useTaskQueues } from "@tolkovo/api-client/react";
import { Button, Label, Select, cn } from "@tolkovo/ui";
import { useId, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

import { describeApiError } from "../../lib/api-errors";
import { TASK_PRIORITIES } from "../../lib/tasks/tasks";
import { FormMessage, TextField } from "../form/fields";
import { control } from "./task-bits";

type NewTaskFormProps = {
  /** Where the form starts: empty by hand, the draft from a conversation. */
  initial?: { title: string; body: string };
  /** The conversation the task is created from: it waits for the task. */
  conversationId?: string;
  onDone: (task: Task) => void;
  onCancel: () => void;
};

/** A new task (ADR-0015): title, description, queue, priority and assignee. */
export function NewTaskForm({ initial, conversationId, onDone, onCancel }: NewTaskFormProps) {
  const { t } = useTranslation();
  const create = useCreateTask();
  const { data: members } = useMembers();
  const { data: queues } = useTaskQueues();
  const [title, setTitle] = useState(initial?.title ?? "");
  const [body, setBody] = useState(initial?.body ?? "");
  const [queue, setQueue] = useState("");
  const [priority, setPriority] = useState<TaskPriority>("normal");
  const [assignee, setAssignee] = useState("");
  const [titleError, setTitleError] = useState<string | undefined>();
  const ids = { body: useId(), queue: useId(), priority: useId(), assignee: useId() };

  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (!title.trim()) {
      setTitleError(t("tasks.titleRequired"));
      document.getElementById("task-title-input")?.focus();
      return;
    }
    setTitleError(undefined);
    create.mutate(
      {
        title: title.trim(),
        body,
        priority,
        queue_id: queue || null,
        assignee_id: assignee || null,
        ...(conversationId ? { conversation_id: conversationId } : {}),
      },
      { onSuccess: onDone },
    );
  };

  return (
    <form noValidate onSubmit={submit} className="flex max-w-[760px] flex-col gap-4">
      <TextField
        id="task-title-input"
        name="title"
        label={t("tasks.fields.title")}
        value={title}
        maxLength={256}
        error={titleError}
        onChange={(e) => setTitle(e.currentTarget.value.replace(/[\r\n]+/g, " "))}
      />
      <div className="flex flex-col gap-1.5">
        <Label htmlFor={ids.body}>{t("tasks.fields.body")}</Label>
        <textarea
          id={ids.body}
          value={body}
          rows={10}
          maxLength={20000}
          onChange={(e) => setBody(e.currentTarget.value)}
          className={cn(control, "py-2 leading-relaxed")}
        />
      </div>
      <div className="grid grid-cols-3 gap-3 max-nav:grid-cols-1">
        <div className="flex flex-col gap-1.5">
          <Label htmlFor={ids.queue}>{t("tasks.fields.queue")}</Label>
          <Select id={ids.queue} value={queue} onChange={(e) => setQueue(e.currentTarget.value)}>
            <option value="">{t("tasks.noQueue")}</option>
            {queues?.map((q) => (
              <option key={q.id} value={q.id}>
                {q.name}
              </option>
            ))}
          </Select>
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor={ids.priority}>{t("tasks.fields.priority")}</Label>
          <Select id={ids.priority} value={priority} onChange={(e) => setPriority(e.currentTarget.value as TaskPriority)}>
            {TASK_PRIORITIES.map((p) => (
              <option key={p} value={p}>
                {t(`tasks.priority.${p}`)}
              </option>
            ))}
          </Select>
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor={ids.assignee}>{t("tasks.fields.assignee")}</Label>
          <Select id={ids.assignee} value={assignee} onChange={(e) => setAssignee(e.currentTarget.value)}>
            <option value="">{t("tasks.unassigned")}</option>
            {members?.map((m) => (
              <option key={m.user_id} value={m.user_id}>
                {m.name}
              </option>
            ))}
          </Select>
        </div>
      </div>
      <p className="m-0 text-[12.5px] text-ink-3">{t(assignee ? "tasks.notifyAssignee" : "tasks.notifyQueue")}</p>
      <FormMessage tone="error" message={create.isError ? describeApiError(t, create.error).message : null} />
      <div className="flex flex-wrap gap-2">
        <Button type="submit" variant="primary" disabled={create.isPending}>
          {create.isPending ? t("tasks.creating") : t("tasks.create")}
        </Button>
        <Button type="button" onClick={onCancel}>
          {t("tasks.cancel")}
        </Button>
      </div>
    </form>
  );
}
