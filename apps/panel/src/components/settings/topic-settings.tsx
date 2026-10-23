import type { TagColor, Topic } from "@tolkovo/api-client";
import { useCan, useMembers, useTaskQueues, useTopicMutations, useTopics } from "@tolkovo/api-client/react";
import { Button, Skeleton, cn } from "@tolkovo/ui";
import { ArrowDown, ArrowUp } from "lucide-react";
import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

import { describeApiError } from "../../lib/api-errors";
import { answersCustomers } from "../../lib/team";
import { FormMessage, SelectField, TextField } from "../form/fields";
import { TAG_DOT } from "../inbox/palette";

const COLORS: readonly TagColor[] = ["gray", "teal", "amber", "red", "violet", "blue", "green"];
const PRIORITIES = [0, 1, 2, 3] as const;
/** At most this many topics (TopicList in api/openapi.yaml): a topic is a button in the bot. */
const MAX_TOPICS = 20;

/**
 * «Темы вопросов» (docs/02 §7.3): the customer picks one in the Telegram bot or the widget before
 * writing. Each topic sets the priority of its conversations, who gets them from automatic
 * distribution and the queue of tasks made from them.
 */
export function TopicSettingsPage() {
  const { t } = useTranslation();
  const topics = useTopics();
  const [adding, setAdding] = useState(false);
  const list = topics.data ?? [];
  return (
    <section aria-labelledby="settings-topics-title" aria-busy={topics.isPending} className="flex max-w-[720px] flex-col gap-3">
      <div className="flex items-center gap-2">
        <h2 id="settings-topics-title" className="m-0 mr-auto text-base font-semibold">
          {t("settings.topics.title")}
        </h2>
        {!adding && topics.isSuccess && list.length < MAX_TOPICS && (
          <Button size="sm" onClick={() => setAdding(true)}>
            {t("settings.topics.add")}
          </Button>
        )}
      </div>
      <p className="m-0 text-[13px] text-ink-2">{t("settings.topics.lead")}</p>
      {adding && <TopicForm onDone={() => setAdding(false)} />}
      {topics.isPending ? (
        <div role="status">
          <span className="sr-only">{t("app.loading")}</span>
          <Skeleton className="h-24" />
        </div>
      ) : topics.isError ? (
        <div role="alert" className="flex flex-col items-start gap-2">
          <p className="m-0 text-ink-2">{describeApiError(t, topics.error).message}</p>
          <Button onClick={() => void topics.refetch()}>{t("error.retry")}</Button>
        </div>
      ) : list.length === 0 && !adding ? (
        <p className="m-0 text-[13px] text-ink-3">{t("settings.topics.empty")}</p>
      ) : (
        <ol className="m-0 flex list-none flex-col gap-2 p-0">
          {list.map((topic, i) => (
            <TopicItem key={topic.id} topic={topic} order={list.map((x) => x.id)} index={i} />
          ))}
        </ol>
      )}
    </section>
  );
}

function TopicItem({ topic, order, index }: { topic: Topic; order: string[]; index: number }) {
  const { t } = useTranslation();
  const { data: members } = useMembers();
  const { data: queues } = useTaskQueues({ enabled: useCan("tasks.view") });
  const { remove, reorder } = useTopicMutations();
  const [editing, setEditing] = useState(false);
  const [confirming, setConfirming] = useState(false);
  if (editing) {
    return (
      <li>
        <TopicForm topic={topic} onDone={() => setEditing(false)} />
      </li>
    );
  }
  const move = (by: number) => {
    const next = [...order];
    const [id] = next.splice(index, 1);
    if (id === undefined) return;
    next.splice(index + by, 0, id);
    reorder.mutate(next);
  };
  const names = topic.member_ids.map((id) => members?.find((m) => m.user_id === id)?.name).filter(Boolean);
  const queue = queues?.find((q) => q.id === topic.task_queue_id);
  return (
    <li className="flex flex-wrap items-center gap-2 rounded-lg border border-line bg-surface px-3 py-2 text-[13px]">
      <span className="flex flex-col">
        <button
          type="button"
          aria-label={t("settings.topics.up", { name: topic.name })}
          disabled={index === 0 || reorder.isPending}
          onClick={() => move(-1)}
          className="grid size-5 place-items-center rounded text-ink-3 hover:bg-hover hover:text-ink disabled:invisible"
        >
          <ArrowUp aria-hidden="true" className="size-3.5" />
        </button>
        <button
          type="button"
          aria-label={t("settings.topics.down", { name: topic.name })}
          disabled={index === order.length - 1 || reorder.isPending}
          onClick={() => move(1)}
          className="grid size-5 place-items-center rounded text-ink-3 hover:bg-hover hover:text-ink disabled:invisible"
        >
          <ArrowDown aria-hidden="true" className="size-3.5" />
        </button>
      </span>
      <span className="flex min-w-0 flex-1 flex-col gap-0.5">
        <span className="inline-flex items-center gap-1.5 font-medium">
          <span aria-hidden="true" className={cn("size-2 rounded-full", TAG_DOT[topic.color])} />
          {topic.name}
        </span>
        <span className="text-[12.5px] text-ink-2">
          {t(`settings.topics.priorities.${topic.priority as (typeof PRIORITIES)[number]}`)}
          {" · "}
          {names.length > 0 ? names.join(", ") : t("settings.topics.everyone")}
          {queue && ` · ${t("settings.topics.queueShort", { name: queue.name })}`}
        </span>
      </span>
      {confirming ? (
        <span className="flex items-center gap-2">
          <span className="text-ink-2">{t("settings.topics.deleteConfirm")}</span>
          <Button size="sm" variant="destructive" disabled={remove.isPending} onClick={() => remove.mutate(topic.id)}>
            {t("settings.topics.deleteSubmit")}
          </Button>
          <Button size="sm" onClick={() => setConfirming(false)}>
            {t("tasks.cancel")}
          </Button>
        </span>
      ) : (
        <>
          <Button size="sm" variant="ghost" onClick={() => setEditing(true)}>
            {t("settings.topics.edit")}
          </Button>
          <Button size="sm" variant="ghost" className="text-danger" onClick={() => setConfirming(true)}>
            {t("settings.topics.delete")}
          </Button>
        </>
      )}
      {(remove.isError || reorder.isError) && (
        <p role="alert" className="m-0 w-full text-danger">
          {describeApiError(t, remove.error ?? reorder.error).message}
        </p>
      )}
    </li>
  );
}

function TopicForm({ topic, onDone }: { topic?: Topic; onDone: () => void }) {
  const { t } = useTranslation();
  const { data: members } = useMembers();
  const canQueues = useCan("tasks.view");
  const { data: queues } = useTaskQueues({ enabled: canQueues });
  const { create, update } = useTopicMutations();
  const save = topic ? update : create;
  const [name, setName] = useState(topic?.name ?? "");
  const [color, setColor] = useState<TagColor>(topic?.color ?? "gray");
  const [priority, setPriority] = useState(topic?.priority ?? 0);
  const [queueId, setQueueId] = useState(topic?.task_queue_id ?? "");
  const [memberIds, setMemberIds] = useState<string[]>(topic?.member_ids ?? []);
  const [nameError, setNameError] = useState<string | undefined>();
  const key = topic?.id ?? "new";

  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (!name.trim()) {
      setNameError(t("settings.topics.nameRequired"));
      document.getElementById(`topic-name-${key}`)?.focus();
      return;
    }
    setNameError(undefined);
    const body = { name: name.trim(), color, priority, task_queue_id: queueId || null, member_ids: memberIds };
    if (topic) update.mutate({ id: topic.id, body }, { onSuccess: onDone });
    else create.mutate(body, { onSuccess: onDone });
  };

  const error = save.isError ? describeApiError(t, save.error, { topic_exists: "name" }) : null;
  return (
    <form noValidate onSubmit={submit} className="flex flex-col gap-3 rounded-lg border border-teal bg-surface p-3">
      <TextField
        id={`topic-name-${key}`}
        name="name"
        label={t("settings.topics.name")}
        hint={t("settings.topics.nameHint")}
        value={name}
        maxLength={40}
        error={nameError ?? (error?.field === "name" ? error.message : undefined)}
        onChange={(e) => setName(e.currentTarget.value)}
      />
      <fieldset className="m-0 flex flex-wrap items-center gap-2 border-0 p-0">
        <legend className="mb-1 p-0 text-[13px] font-medium">{t("settings.topics.color")}</legend>
        {COLORS.map((c) => (
          <label key={c} className="cursor-pointer">
            <input type="radio" name={`topic-color-${key}`} checked={color === c} onChange={() => setColor(c)} className="peer sr-only" />
            <span
              className={cn(
                "grid size-7 place-items-center rounded-full border border-line peer-checked:border-ink peer-focus-visible:outline-2 peer-focus-visible:outline-teal",
              )}
            >
              <span aria-hidden="true" className={cn("size-3 rounded-full", TAG_DOT[c])} />
              <span className="sr-only">{t(`settings.topics.colors.${c}`)}</span>
            </span>
          </label>
        ))}
      </fieldset>
      <SelectField
        id={`topic-priority-${key}`}
        name="priority"
        label={t("settings.topics.priority")}
        hint={t("settings.topics.priorityHint")}
        value={String(priority)}
        onChange={(e) => setPriority(Number(e.currentTarget.value))}
      >
        {PRIORITIES.map((p) => (
          <option key={p} value={p}>
            {t(`settings.topics.priorities.${p}`)}
          </option>
        ))}
      </SelectField>
      <fieldset className="m-0 flex flex-col gap-1 border-0 p-0">
        <legend className="mb-1 p-0 text-[13px] font-medium">{t("settings.topics.members")}</legend>
        <p className="m-0 mb-1 text-[12.5px] text-ink-2">{t("settings.topics.membersHint")}</p>
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
      {canQueues && (
        <SelectField
          id={`topic-queue-${key}`}
          name="queue"
          label={t("settings.topics.queue")}
          hint={t("settings.topics.queueHint")}
          value={queueId}
          onChange={(e) => setQueueId(e.currentTarget.value)}
        >
          <option value="">{t("settings.topics.noQueue")}</option>
          {queues?.map((q) => (
            <option key={q.id} value={q.id}>
              {q.name}
            </option>
          ))}
        </SelectField>
      )}
      <FormMessage tone="error" message={error && error.field !== "name" ? error.message : null} />
      <div className="flex gap-2">
        <Button type="submit" size="sm" variant="primary" disabled={save.isPending}>
          {t("settings.topics.save")}
        </Button>
        <Button type="button" size="sm" onClick={onDone}>
          {t("tasks.cancel")}
        </Button>
      </div>
    </form>
  );
}
