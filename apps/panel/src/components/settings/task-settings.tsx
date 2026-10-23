import type { TagColor, TaskButton, TaskQueue, TaskSettingsView, TaskTag } from "@tolkovo/api-client";
import {
  useCreateTaskQueue,
  useCreateTaskTag,
  useDeleteTaskQueue,
  useDeleteTaskTag,
  useDeleteTaskTelegramBot,
  useSetTaskTelegramBot,
  useTaskTelegramBot,
  useMembers,
  useTaskQueues,
  useTaskSettings,
  useTaskTags,
  useUpdateTaskQueue,
  useUpdateTaskSettings,
  useUpdateTaskTag,
} from "@tolkovo/api-client/react";
import { Button, Label, Select, Skeleton, cn } from "@tolkovo/ui";
import { useId, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

import { describeApiError } from "../../lib/api-errors";
import { FormMessage, TextField } from "../form/fields";
import { TAG_DOT } from "../inbox/palette";
import { control } from "../tasks/task-bits";
import { BoardRules } from "./task-board-rules";

const BUTTONS: readonly TaskButton[] = ["both", "tasks", "github"];
const COLORS: readonly TagColor[] = ["gray", "teal", "amber", "red", "violet", "blue", "green"];

/**
 * «Задачи» (ADR-0015): the section on or off, which buttons conversations show, the reply about a
 * done task, the queues with the members told about new tasks in them, the rules of the board
 * (ADR-0019) and the tags of tasks.
 */
export function TaskSettingsPage() {
  const { t } = useTranslation();
  const query = useTaskSettings();
  return (
    <section aria-labelledby="settings-tasks-title" aria-busy={query.isPending} className="flex max-w-[720px] flex-col gap-5">
      <h2 id="settings-tasks-title" className="m-0 text-base font-semibold">
        {t("settings.tasks.title")}
      </h2>
      {query.isPending ? (
        <div role="status">
          <span className="sr-only">{t("app.loading")}</span>
          <Skeleton className="h-48" />
        </div>
      ) : query.isError ? (
        <div role="alert" className="flex flex-col items-start gap-2">
          <p className="m-0 text-ink-2">{describeApiError(t, query.error).message}</p>
          <Button onClick={() => void query.refetch()}>{t("error.retry")}</Button>
        </div>
      ) : (
        <>
          <SettingsForm view={query.data} />
          <Queues />
          <BoardRules />
          <Tags />
          <TelegramBot />
        </>
      )}
    </section>
  );
}

function SettingsForm({ view }: { view: TaskSettingsView }) {
  const { t } = useTranslation();
  const update = useUpdateTaskSettings();
  const [enabled, setEnabled] = useState(view.settings.enabled);
  const [button, setButton] = useState<TaskButton>(view.settings.conversation_button);
  const [replyRu, setReplyRu] = useState(view.settings.reply_ru);
  const [replyEn, setReplyEn] = useState(view.settings.reply_en);
  const [saved, setSaved] = useState<string | null>(null);
  const ids = { ru: useId(), en: useId(), hint: useId() };

  const submit = (e: FormEvent) => {
    e.preventDefault();
    setSaved(null);
    update.mutate(
      { enabled, conversation_button: button, reply_ru: replyRu, reply_en: replyEn },
      { onSuccess: () => setSaved(t("settings.tasks.saved")) },
    );
  };

  return (
    <form noValidate onSubmit={submit} className="flex flex-col gap-4 rounded-xl border border-line bg-surface p-4">
      <label className="flex items-start gap-2.5">
        <input type="checkbox" checked={enabled} onChange={(e) => setEnabled(e.currentTarget.checked)} className="mt-0.5 size-4 shrink-0 accent-teal" />
        <span className="flex flex-col gap-0.5">
          <span className="text-[13px] font-medium">{t("settings.tasks.enabled")}</span>
          <span className="text-[12.5px] text-ink-2">{t("settings.tasks.enabledHint")}</span>
        </span>
      </label>
      <fieldset className="m-0 flex flex-col gap-2 border-0 p-0" disabled={!enabled}>
        <legend className="mb-1 p-0 text-[13px] font-medium">{t("settings.tasks.button")}</legend>
        {BUTTONS.map((b) => (
          <label
            key={b}
            className={cn(
              "flex cursor-pointer items-start gap-2.5 rounded-lg border px-3 py-2",
              button === b ? "border-teal bg-teal-soft" : "border-line hover:bg-hover",
            )}
          >
            <input type="radio" name="task-button" checked={button === b} onChange={() => setButton(b)} className="mt-0.5 size-4 shrink-0 accent-teal" />
            <span className="flex flex-col gap-0.5">
              <span className="text-[13px]">{t(`settings.tasks.buttons.${b}`)}</span>
              <span className="text-[12.5px] text-ink-2">{t(`settings.tasks.buttons.${b}Hint`)}</span>
            </span>
          </label>
        ))}
      </fieldset>
      <div className="flex flex-col gap-1.5">
        <div className="flex items-center gap-2">
          <Label htmlFor={ids.ru} className="mr-auto">
            {t("settings.tasks.replyRu")}
          </Label>
          {replyRu !== view.defaults.reply_ru && (
            <Button type="button" size="sm" variant="ghost" onClick={() => setReplyRu(view.defaults.reply_ru)}>
              {t("settings.tasks.reset")}
            </Button>
          )}
        </div>
        <textarea
          id={ids.ru}
          value={replyRu}
          rows={3}
          maxLength={4000}
          aria-describedby={ids.hint}
          onChange={(e) => setReplyRu(e.currentTarget.value)}
          className={cn(control, "py-2 leading-relaxed")}
        />
      </div>
      <div className="flex flex-col gap-1.5">
        <div className="flex items-center gap-2">
          <Label htmlFor={ids.en} className="mr-auto">
            {t("settings.tasks.replyEn")}
          </Label>
          {replyEn !== view.defaults.reply_en && (
            <Button type="button" size="sm" variant="ghost" onClick={() => setReplyEn(view.defaults.reply_en)}>
              {t("settings.tasks.reset")}
            </Button>
          )}
        </div>
        <textarea
          id={ids.en}
          value={replyEn}
          rows={3}
          maxLength={4000}
          aria-describedby={ids.hint}
          onChange={(e) => setReplyEn(e.currentTarget.value)}
          className={cn(control, "py-2 leading-relaxed")}
        />
        <p id={ids.hint} className="m-0 text-[12.5px] text-ink-3">
          {t("settings.tasks.replyHint", { placeholders: view.placeholders.map((p) => `{${p}}`).join(", ") })}
        </p>
      </div>
      <FormMessage tone="error" message={update.isError ? describeApiError(t, update.error).message : null} />
      <FormMessage tone="success" message={saved} />
      <Button type="submit" variant="primary" className="self-start" disabled={update.isPending}>
        {t("settings.tasks.save")}
      </Button>
    </form>
  );
}

/** Queues: add, rename, pick the members told about new tasks without an assignee, delete. */
function Queues() {
  const { t } = useTranslation();
  const queues = useTaskQueues();
  const [adding, setAdding] = useState(false);
  return (
    <section aria-labelledby="settings-queues-title" className="flex flex-col gap-3">
      <div className="flex items-center gap-2">
        <h3 id="settings-queues-title" className="m-0 mr-auto text-[14px] font-semibold">
          {t("settings.tasks.queues")}
        </h3>
        {!adding && (
          <Button size="sm" onClick={() => setAdding(true)}>
            {t("settings.tasks.addQueue")}
          </Button>
        )}
      </div>
      <p className="m-0 text-[12.5px] text-ink-2">{t("settings.tasks.queuesHint")}</p>
      {adding && <QueueForm onDone={() => setAdding(false)} />}
      {queues.isPending ? (
        <Skeleton className="h-16" />
      ) : queues.isError ? (
        <p role="alert" className="m-0 text-[13px] text-ink-2">
          {describeApiError(t, queues.error).message}
        </p>
      ) : queues.data.length === 0 && !adding ? (
        <p className="m-0 text-[13px] text-ink-3">{t("settings.tasks.noQueues")}</p>
      ) : (
        <ul className="m-0 flex list-none flex-col gap-2 p-0">
          {queues.data.map((q) => (
            <QueueItem key={q.id} queue={q} />
          ))}
        </ul>
      )}
    </section>
  );
}

function QueueItem({ queue }: { queue: TaskQueue }) {
  const { t } = useTranslation();
  const { data: members } = useMembers();
  const remove = useDeleteTaskQueue();
  const [editing, setEditing] = useState(false);
  if (editing) {
    return (
      <li>
        <QueueForm queue={queue} onDone={() => setEditing(false)} />
      </li>
    );
  }
  const names = queue.member_ids.map((id) => members?.find((m) => m.user_id === id)?.name).filter(Boolean);
  return (
    <li className="flex flex-wrap items-center gap-2 rounded-lg border border-line bg-surface px-3 py-2 text-[13px]">
      <span className="flex min-w-0 flex-1 flex-col gap-0.5">
        <span className="font-medium">{queue.name}</span>
        <span className="text-[12.5px] text-ink-2">
          {t("settings.tasks.openTasks", { count: queue.open_tasks })}
          {" · "}
          {names.length > 0 ? names.join(", ") : t("settings.tasks.noMembers")}
        </span>
      </span>
      <Button size="sm" variant="ghost" onClick={() => setEditing(true)}>
        {t("settings.tasks.editQueue")}
      </Button>
      <Button size="sm" variant="ghost" className="text-danger" disabled={remove.isPending} onClick={() => remove.mutate(queue.id)}>
        {t("settings.tasks.deleteQueue")}
      </Button>
      {remove.isError && <p className="m-0 w-full text-danger">{describeApiError(t, remove.error).message}</p>}
    </li>
  );
}

function QueueForm({ queue, onDone }: { queue?: TaskQueue; onDone: () => void }) {
  const { t } = useTranslation();
  const { data: members } = useMembers();
  const create = useCreateTaskQueue();
  const update = useUpdateTaskQueue();
  const save = queue ? update : create;
  const [name, setName] = useState(queue?.name ?? "");
  const [memberIds, setMemberIds] = useState<string[]>(queue?.member_ids ?? []);
  const [nameError, setNameError] = useState<string | undefined>();
  const fieldId = queue ? `queue-name-${queue.id}` : "queue-name-new";

  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (!name.trim()) {
      setNameError(t("settings.tasks.queueNameRequired"));
      document.getElementById(fieldId)?.focus();
      return;
    }
    setNameError(undefined);
    const body = { name: name.trim(), member_ids: memberIds };
    if (queue) update.mutate({ id: queue.id, ...body }, { onSuccess: onDone });
    else create.mutate(body, { onSuccess: onDone });
  };

  const error = save.isError ? describeApiError(t, save.error, { queue_exists: "name" }) : null;
  return (
    <form noValidate onSubmit={submit} className="flex flex-col gap-3 rounded-lg border border-teal bg-surface p-3">
      <TextField
        id={fieldId}
        name="name"
        label={t("settings.tasks.queueName")}
        value={name}
        maxLength={60}
        error={nameError ?? (error?.field === "name" ? error.message : undefined)}
        onChange={(e) => setName(e.currentTarget.value)}
      />
      <fieldset className="m-0 flex flex-col gap-1 border-0 p-0">
        <legend className="mb-1 p-0 text-[13px] font-medium">{t("settings.tasks.queueMembers")}</legend>
        {members
          ?.filter((m) => m.active)
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
          {t("settings.tasks.saveQueue")}
        </Button>
        <Button type="button" size="sm" onClick={onDone}>
          {t("tasks.cancel")}
        </Button>
      </div>
    </form>
  );
}

/** Tags of tasks: any member adds them from a task; here admins add, rename, recolor and delete them. */
function Tags() {
  const { t } = useTranslation();
  const tags = useTaskTags();
  const [adding, setAdding] = useState(false);
  return (
    <section aria-labelledby="settings-task-tags-title" className="flex flex-col gap-3">
      <div className="flex items-center gap-2">
        <h3 id="settings-task-tags-title" className="m-0 mr-auto text-[14px] font-semibold">
          {t("settings.tasks.tags")}
        </h3>
        {!adding && (
          <Button size="sm" onClick={() => setAdding(true)}>
            {t("settings.tasks.addTag")}
          </Button>
        )}
      </div>
      <p className="m-0 text-[12.5px] text-ink-2">{t("settings.tasks.tagsHint")}</p>
      {adding && <TagForm onDone={() => setAdding(false)} />}
      {tags.isPending ? (
        <Skeleton className="h-16" />
      ) : tags.isError ? (
        <p role="alert" className="m-0 text-[13px] text-ink-2">
          {describeApiError(t, tags.error).message}
        </p>
      ) : tags.data.length === 0 && !adding ? (
        <p className="m-0 text-[13px] text-ink-3">{t("settings.tasks.noTags")}</p>
      ) : (
        <ul className="m-0 flex list-none flex-col gap-2 p-0">
          {tags.data.map((tag) => (
            <TagItem key={tag.id} tag={tag} />
          ))}
        </ul>
      )}
    </section>
  );
}

function TagItem({ tag }: { tag: TaskTag }) {
  const { t } = useTranslation();
  const remove = useDeleteTaskTag();
  const [editing, setEditing] = useState(false);
  const [confirm, setConfirm] = useState(false);
  if (editing) {
    return (
      <li>
        <TagForm tag={tag} onDone={() => setEditing(false)} />
      </li>
    );
  }
  return (
    <li className="flex flex-wrap items-center gap-2 rounded-lg border border-line bg-surface px-3 py-2 text-[13px]">
      <span aria-hidden="true" className={cn("size-2.5 shrink-0 rounded-full", TAG_DOT[tag.color])} />
      <span className="mr-auto min-w-0 font-medium break-words">{tag.name}</span>
      {confirm ? (
        <>
          <span className="text-ink-2">{t("settings.tasks.deleteTagConfirm")}</span>
          <Button size="sm" variant="destructive" disabled={remove.isPending} onClick={() => remove.mutate(tag.id)}>
            {t("settings.tasks.deleteTag")}
          </Button>
          <Button size="sm" onClick={() => setConfirm(false)}>
            {t("tasks.cancel")}
          </Button>
        </>
      ) : (
        <>
          <Button size="sm" variant="ghost" onClick={() => setEditing(true)}>
            {t("settings.tasks.editTag")}
          </Button>
          <Button size="sm" variant="ghost" className="text-danger" onClick={() => setConfirm(true)}>
            {t("settings.tasks.deleteTag")}
          </Button>
        </>
      )}
      {remove.isError && <p className="m-0 w-full text-danger">{describeApiError(t, remove.error).message}</p>}
    </li>
  );
}

function TagForm({ tag, onDone }: { tag?: TaskTag; onDone: () => void }) {
  const { t } = useTranslation();
  const create = useCreateTaskTag();
  const update = useUpdateTaskTag();
  const save = tag ? update : create;
  const [name, setName] = useState(tag?.name ?? "");
  const [color, setColor] = useState<TagColor>(tag?.color ?? "gray");
  const [nameError, setNameError] = useState<string | undefined>();
  const fieldId = tag ? `task-tag-name-${tag.id}` : "task-tag-name-new";
  const colorId = useId();

  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (!name.trim()) {
      setNameError(t("settings.tasks.tagNameRequired"));
      document.getElementById(fieldId)?.focus();
      return;
    }
    setNameError(undefined);
    const body = { name: name.trim(), color };
    if (tag) update.mutate({ id: tag.id, ...body }, { onSuccess: onDone });
    else create.mutate(body, { onSuccess: onDone });
  };

  const error = save.isError ? describeApiError(t, save.error, { tag_exists: "name" }) : null;
  return (
    <form noValidate onSubmit={submit} className="flex flex-col gap-3 rounded-lg border border-teal bg-surface p-3">
      <TextField
        id={fieldId}
        name="name"
        label={t("settings.tasks.tagName")}
        value={name}
        maxLength={40}
        error={nameError ?? (error?.field === "name" ? error.message : undefined)}
        onChange={(e) => setName(e.currentTarget.value)}
      />
      <div className="flex flex-col gap-1.5">
        <Label htmlFor={colorId}>{t("settings.tasks.tagColor")}</Label>
        <div className="flex items-center gap-2">
          <span aria-hidden="true" className={cn("size-3 shrink-0 rounded-full", TAG_DOT[color])} />
          <Select id={colorId} value={color} onChange={(e) => setColor(e.currentTarget.value as TagColor)}>
            {COLORS.map((c) => (
              <option key={c} value={c}>
                {t(`settings.tasks.colors.${c}`)}
              </option>
            ))}
          </Select>
        </div>
      </div>
      <FormMessage tone="error" message={error && error.field !== "name" ? error.message : null} />
      <div className="flex gap-2">
        <Button type="submit" size="sm" variant="primary" disabled={save.isPending}>
          {t("settings.tasks.saveTag")}
        </Button>
        <Button type="button" size="sm" onClick={onDone}>
          {t("tasks.cancel")}
        </Button>
      </div>
    </form>
  );
}

/**
 * The Telegram bot for notifications (TK-02): a bot of its own, not the bot of a support channel.
 * Members link their Telegram to it in their notification settings.
 */
function TelegramBot() {
  const { t } = useTranslation();
  const bot = useTaskTelegramBot();
  const set = useSetTaskTelegramBot();
  const remove = useDeleteTaskTelegramBot();
  const [token, setToken] = useState("");
  const [replacing, setReplacing] = useState(false);
  const [tokenError, setTokenError] = useState<string | undefined>();
  const failure = set.error ?? remove.error;
  const described = failure ? describeApiError(t, failure, { telegram_token_invalid: "token" }) : null;

  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (!token.trim()) {
      setTokenError(t("settings.tasks.bot.tokenRequired"));
      document.getElementById("task-bot-token")?.focus();
      return;
    }
    setTokenError(undefined);
    set.mutate(token.trim(), {
      onSuccess: () => {
        setToken("");
        setReplacing(false);
      },
    });
  };

  return (
    <section aria-labelledby="settings-task-bot-title" className="flex flex-col gap-3">
      <h3 id="settings-task-bot-title" className="m-0 text-[14px] font-semibold">
        {t("settings.tasks.bot.title")}
      </h3>
      <p className="m-0 text-[12.5px] text-ink-2">{t("settings.tasks.bot.lead")}</p>
      {bot.isPending ? (
        <Skeleton className="h-16" />
      ) : bot.isError ? (
        <p role="alert" className="m-0 text-[13px] text-ink-2">
          {describeApiError(t, bot.error).message}
        </p>
      ) : !bot.data.encryption_available ? (
        <p className="m-0 rounded-lg border border-line bg-paper px-3 py-2 text-[13px] text-ink-2">{t("settings.tasks.bot.noEncryption")}</p>
      ) : (
        <div className="flex flex-col gap-3 rounded-xl border border-line bg-surface p-4 text-[13px]">
          {bot.data.bot && !replacing ? (
            <div className="flex flex-wrap items-center gap-2">
              <p className="m-0 mr-auto">{t("settings.tasks.bot.connected", { bot: bot.data.bot.username })}</p>
              <Button size="sm" onClick={() => setReplacing(true)}>
                {t("settings.tasks.bot.replace")}
              </Button>
              <Button size="sm" variant="ghost" className="text-danger" disabled={remove.isPending} onClick={() => remove.mutate()}>
                {t("settings.tasks.bot.remove")}
              </Button>
            </div>
          ) : (
            <form noValidate onSubmit={submit} className="flex flex-col gap-3">
              <p className="m-0 rounded-lg border border-note-line bg-note px-3 py-2 text-ink">{t("settings.tasks.bot.dataNote")}</p>
              <TextField
                id="task-bot-token"
                name="token"
                type="password"
                autoComplete="off"
                label={t("settings.tasks.bot.token")}
                hint={t("settings.tasks.bot.tokenHint")}
                value={token}
                error={tokenError ?? (described?.field === "token" ? described.message : undefined)}
                onChange={(e) => setToken(e.currentTarget.value)}
              />
              <div className="flex gap-2">
                <Button type="submit" size="sm" variant="primary" disabled={set.isPending}>
                  {t("settings.tasks.bot.save")}
                </Button>
                {replacing && (
                  <Button type="button" size="sm" onClick={() => setReplacing(false)}>
                    {t("tasks.cancel")}
                  </Button>
                )}
              </div>
            </form>
          )}
          <FormMessage tone="error" message={described && described.field !== "token" ? described.message : null} />
        </div>
      )}
    </section>
  );
}
