import type { KBArticle, Task, TaskArticle } from "@tolkovo/api-client";
import { useAddTaskArticle, useCan, useKBArticleTasks, useKBArticles, useRemoveTaskArticle, useTaskArticles } from "@tolkovo/api-client/react";
import { Link } from "@tanstack/react-router";
import { Button, Label, Skeleton, cn } from "@tolkovo/ui";
import { BookOpen, ChevronRight, Plus, X } from "lucide-react";
import { useEffect, useId, useRef, useState } from "react";
import { useTranslation } from "react-i18next";

import { describeApiError } from "../../lib/api-errors";
import { FormMessage } from "../form/fields";
import { TaskStatusBadge, control } from "./task-bits";

/** How many matches the picker offers at once; the search narrows the rest. */
const PICK_LIMIT = 8;

/** The anchor of an article in «Настройки → База знаний». */
export function kbArticleAnchor(id: string): string {
  return `kb-article-${id}`;
}

/**
 * Articles of the knowledge base linked to the task (ADR-0022, P5): what to read before the work
 * and what to update after it. Who keeps the base links them; who sees the task reads the titles.
 */
export function TaskArticles({ task }: { task: Task }) {
  const { t } = useTranslation();
  const articles = useTaskArticles(task.id);
  const canEdit = useCan("tasks.edit");
  const canKB = useCan("knowledge.manage");
  const canLink = canEdit && canKB;
  const [adding, setAdding] = useState(false);
  const addButton = useRef<HTMLButtonElement>(null);
  const items = articles.data ?? [];
  if (!canLink && items.length === 0) return null;
  return (
    <section aria-labelledby="task-articles" className="flex flex-col gap-2">
      <div className="flex items-center gap-2">
        <h3 id="task-articles" className="m-0 mr-auto text-[13px] font-semibold">
          {t("tasks.articles.title")}
        </h3>
        {canLink && !adding && (
          <Button ref={addButton} size="sm" variant="ghost" onClick={() => setAdding(true)}>
            <Plus aria-hidden="true" className="size-4" />
            {t("tasks.articles.add")}
          </Button>
        )}
      </div>
      {articles.isPending ? (
        <Skeleton className="h-9" />
      ) : articles.isError ? (
        <p role="alert" className="m-0 text-[13px] text-ink-2">
          {t("tasks.articles.loadError")}
        </p>
      ) : items.length === 0 ? (
        !adding && <p className="m-0 text-[13px] text-ink-3">{t("tasks.articles.none")}</p>
      ) : (
        <ul className="m-0 flex list-none flex-col gap-1.5 p-0">
          {items.map((a) => (
            <ArticleRow key={a.id} taskId={task.id} article={a} canOpen={canKB} canRemove={canLink} />
          ))}
        </ul>
      )}
      {adding && (
        <ArticlePicker
          task={task}
          linked={items}
          onClose={() => {
            setAdding(false);
            requestAnimationFrame(() => addButton.current?.focus());
          }}
        />
      )}
    </section>
  );
}

function ArticleRow({ taskId, article: a, canOpen, canRemove }: { taskId: string; article: TaskArticle; canOpen: boolean; canRemove: boolean }) {
  const { t } = useTranslation();
  const remove = useRemoveTaskArticle();
  return (
    <li className="flex min-w-0 flex-col gap-1 rounded-lg border border-line bg-surface px-3 py-2 text-[13px]">
      <span className="flex min-w-0 items-center gap-2">
        <BookOpen aria-hidden="true" className="size-4 shrink-0 text-ink-3" />
        {canOpen ? (
          <Link to="/settings/knowledge" hash={kbArticleAnchor(a.id)} className="mr-auto min-w-0 truncate text-ink">
            {a.title}
          </Link>
        ) : (
          <span className="mr-auto min-w-0 truncate">{a.title}</span>
        )}
        {a.kind === "qa" && <span className="shrink-0 text-[12px] text-ink-3">{t("settings.knowledge.kindQa")}</span>}
        {!a.published && (
          <span className="shrink-0 rounded bg-lamp-soft px-1.5 py-px text-[11.5px] text-ink">{t("settings.knowledge.draft")}</span>
        )}
        {canRemove && (
          <button
            type="button"
            aria-label={t("tasks.articles.remove", { title: a.title })}
            title={t("tasks.articles.removeTitle")}
            disabled={remove.isPending}
            onClick={() => remove.mutate({ taskId, articleId: a.id })}
            className="grid size-7 shrink-0 place-items-center rounded-md text-ink-3 hover:bg-hover hover:text-ink"
          >
            <X aria-hidden="true" className="size-4" />
          </button>
        )}
      </span>
      {remove.isError && (
        <p role="alert" className="m-0 text-[12.5px] text-danger">
          {describeApiError(t, remove.error).message}
        </p>
      )}
    </li>
  );
}

/** A search over the titles of the base; a click links the article. */
function ArticlePicker({ task, linked, onClose }: { task: Task; linked: TaskArticle[]; onClose: () => void }) {
  const { t } = useTranslation();
  const base = useKBArticles();
  const add = useAddTaskArticle();
  const [query, setQuery] = useState("");
  const ids = { q: useId(), list: useId() };
  const field = useRef<HTMLInputElement>(null);
  useEffect(() => field.current?.focus(), []);
  const taken = new Set(linked.map((a) => a.id));
  const q = query.trim().toLocaleLowerCase();
  const matches: KBArticle[] = (base.data ?? []).filter((a) => !taken.has(a.id) && (q === "" || a.title.toLocaleLowerCase().includes(q)));
  return (
    <div
      role="group"
      aria-label={t("tasks.articles.add")}
      onKeyDown={(e) => {
        if (e.key === "Escape") {
          e.preventDefault();
          onClose();
        }
      }}
      className="flex flex-col gap-2 rounded-lg border border-line bg-surface p-3"
    >
      <div className="flex flex-col gap-1.5">
        <Label htmlFor={ids.q}>{t("tasks.articles.search")}</Label>
        <input
          id={ids.q}
          ref={field}
          type="search"
          value={query}
          autoComplete="off"
          aria-controls={ids.list}
          placeholder={t("tasks.articles.searchPlaceholder")}
          onChange={(e) => setQuery(e.currentTarget.value)}
          className={cn(control, "h-9 w-full")}
        />
      </div>
      {base.isPending ? (
        <Skeleton className="h-16" />
      ) : base.isError ? (
        <FormMessage tone="error" message={describeApiError(t, base.error).message} />
      ) : matches.length === 0 ? (
        <p id={ids.list} className="m-0 text-[13px] text-ink-3">
          {t(base.data.length === 0 ? "tasks.articles.baseEmpty" : "tasks.articles.nothing")}
        </p>
      ) : (
        <ul id={ids.list} aria-label={t("tasks.articles.found")} className="m-0 flex list-none flex-col gap-0.5 p-0">
          {matches.slice(0, PICK_LIMIT).map((a) => (
            <li key={a.id}>
              <button
                type="button"
                disabled={add.isPending}
                onClick={() => add.mutate({ taskId: task.id, articleId: a.id }, { onSuccess: onClose })}
                className="flex w-full min-w-0 items-center gap-2 rounded-md px-2 py-1.5 text-left text-[13px] hover:bg-hover disabled:opacity-55"
              >
                <BookOpen aria-hidden="true" className="size-4 shrink-0 text-ink-3" />
                <span className="truncate">{a.title}</span>
                {!a.published && <span className="ml-auto shrink-0 text-[11.5px] text-ink-3">{t("settings.knowledge.draft")}</span>}
              </button>
            </li>
          ))}
          {matches.length > PICK_LIMIT && (
            <li className="px-2 pt-1 text-[12px] text-ink-3">{t("tasks.articles.more", { count: matches.length - PICK_LIMIT })}</li>
          )}
        </ul>
      )}
      <FormMessage tone="error" message={add.isError ? describeApiError(t, add.error).message : null} />
      <div>
        <Button type="button" size="sm" onClick={onClose}>
          {t("tasks.cancel")}
        </Button>
      </div>
    </div>
  );
}

/**
 * «Задачи по статье» under an entry of the base: folded until asked, since the base holds up to a
 * thousand entries. A task the member does not see is only counted.
 */
export function KBArticleTasks({ articleId, title }: { articleId: string; title: string }) {
  const { t } = useTranslation();
  const canView = useCan("tasks.view");
  const [open, setOpen] = useState(false);
  const tasks = useKBArticleTasks(articleId, { enabled: canView && open });
  const bodyId = useId();
  if (!canView) return null;
  return (
    <div className="w-full">
      <button
        type="button"
        aria-expanded={open}
        aria-controls={bodyId}
        aria-label={t("tasks.articles.tasksOf", { title })}
        onClick={() => setOpen((v) => !v)}
        className="flex items-center gap-1 text-[12.5px] text-ink-2 hover:text-ink"
      >
        <ChevronRight aria-hidden="true" className={cn("size-3.5 transition-transform", open && "rotate-90")} />
        {t("tasks.articles.tasks")}
      </button>
      <div id={bodyId} hidden={!open} className="pt-1.5 pl-4.5">
        {!open ? null : tasks.isPending ? (
          <Skeleton className="h-6" />
        ) : tasks.isError ? (
          <p role="alert" className="m-0 text-[12.5px] text-ink-2">
            {describeApiError(t, tasks.error).message}
          </p>
        ) : tasks.data.items.length === 0 && tasks.data.hidden === 0 ? (
          <p className="m-0 text-[12.5px] text-ink-3">{t("tasks.articles.noTasks")}</p>
        ) : (
          <>
            <ul className="m-0 flex list-none flex-col gap-1.5 p-0">
              {tasks.data.items.map((task) => (
                <li key={task.id} className="flex min-w-0 items-start gap-2 text-[13px]">
                  <TaskStatusBadge status={task.status} />
                  <Link to="/tasks/$taskId" params={{ taskId: task.id }} className="min-w-0 break-words text-ink">
                    <span className="font-mono text-ink-2">{task.ref}</span> {task.title}
                  </Link>
                </li>
              ))}
            </ul>
            {tasks.data.hidden > 0 && (
              <p className="m-0 mt-1.5 text-[12.5px] text-ink-3">{t("tasks.articles.hiddenTasks", { count: tasks.data.hidden })}</p>
            )}
          </>
        )}
      </div>
    </div>
  );
}
