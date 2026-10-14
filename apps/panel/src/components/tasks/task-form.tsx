import type { Task, TaskPriority } from "@tolkovo/api-client";
import { useCreateTask, useMembers, useTaskQueues, useTopics } from "@tolkovo/api-client/react";
import { Button, Label, Select, cn } from "@tolkovo/ui";
import { useId, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

import { describeApiError } from "../../lib/api-errors";
import { TASK_PRIORITIES } from "../../lib/tasks/tasks";
import { FormMessage, TextField } from "../form/fields";
import { control } from "./task-bits";
import { AttachArea, useFileDrafts } from "./task-files";
import { TaskTagsEditor } from "./task-tags";

type NewTaskFormProps = {
  /** Where the form starts: empty by hand, the draft from a conversation. */
  initial?: { title: string; body: string };
  /** The conversation the task is created from: it waits for the task. */
  conversationId?: string;
  onDone: (task: Task) => void;
  onCancel: () => void;
};

/**
 * A new task (ADR-0015): title, description, topic, queue, priority, assignee and tags. A task from a
 * conversation takes the conversation's topic and the topic's queue unless they are picked here.
 */
export function NewTaskForm({ initial, conversationId, onDone, onCancel }: NewTaskFormProps) {
  const { t } = useTranslation();
  const create = useCreateTask();
  const { data: members } = useMembers();
  const { data: queues } = useTaskQueues();
  const { data: topics } = useTopics();
  const [topic, setTopic] = useState("");
  const [title, setTitle] = useState(initial?.title ?? "");
  const [body, setBody] = useState(initial?.body ?? "");
  const [queue, setQueue] = useState("");
  const [priority, setPriority] = useState<TaskPriority>("normal");
  const [assignee, setAssignee] = useState("");
  const [tags, setTags] = useState<string[]>([]);
  const [titleError, setTitleError] = useState<string | undefined>();
  const files = useFileDrafts();
  const ids = { body: useId(), topic: useId(), queue: useId(), priority: useId(), assignee: useId() };

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
        ...(topic ? { topic_id: topic } : {}),
        assignee_id: assignee || null,
        ...(conversationId ? { conversation_id: conversationId } : {}),
        ...(files.ids.length > 0 ? { file_ids: files.ids } : {}),
        ...(tags.length > 0 ? { tag_ids: tags } : {}),
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
        <AttachArea drafts={files}>
          <textarea
            id={ids.body}
            value={body}
            rows={10}
            maxLength={20000}
            onChange={(e) => setBody(e.currentTarget.value)}
            className={cn(control, "py-2 leading-relaxed")}
          />
        </AttachArea>
      </div>
      <div className="grid grid-cols-2 gap-3 max-nav:grid-cols-1">
        {(topics?.length ?? 0) > 0 && (
          <div className="flex flex-col gap-1.5">
            <Label htmlFor={ids.topic}>{t("tasks.fields.topic")}</Label>
            <Select id={ids.topic} value={topic} onChange={(e) => setTopic(e.currentTarget.value)}>
              <option value="">{t(conversationId ? "tasks.topicOfConversation" : "inbox.topic.none")}</option>
              {topics?.map((x) => (
                <option key={x.id} value={x.id}>
                  {x.name}
                </option>
              ))}
            </Select>
          </div>
        )}
        <div className="flex flex-col gap-1.5">
          <Label htmlFor={ids.queue}>{t("tasks.fields.queue")}</Label>
          <Select id={ids.queue} value={queue} onChange={(e) => setQueue(e.currentTarget.value)}>
            <option value="">{t(topic || conversationId ? "tasks.queueOfTopic" : "tasks.noQueue")}</option>
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
      <div role="group" aria-labelledby="task-form-tags" className="flex flex-col gap-1.5">
        <span id="task-form-tags" className="text-[13px] font-medium">
          {t("tasks.tags.label")}
        </span>
        <TaskTagsEditor selected={tags} onChange={setTags} />
      </div>
      <p className="m-0 text-[12.5px] text-ink-3">{t(assignee ? "tasks.notifyAssignee" : "tasks.notifyQueue")}</p>
      <FormMessage tone="error" message={create.isError ? describeApiError(t, create.error).message : null} />
      <div className="flex flex-wrap gap-2">
        <Button type="submit" variant="primary" disabled={create.isPending || files.uploading}>
          {create.isPending ? t("tasks.creating") : t("tasks.create")}
        </Button>
        <Button type="button" onClick={onCancel}>
          {t("tasks.cancel")}
        </Button>
      </div>
    </form>
  );
}
