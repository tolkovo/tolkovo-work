import type { Task } from "@tolkovo/api-client";
import { useTaskQueues, useTasks, useTaskSummary, useTopics } from "@tolkovo/api-client/react";
import { Link, useNavigate } from "@tanstack/react-router";
import { Button, Select, Skeleton, cn } from "@tolkovo/ui";
import { Plus, Search } from "lucide-react";
import { useEffect, useId, useState } from "react";
import { useTranslation } from "react-i18next";

import { TASK_VIEWS, taskFilter, type TasksSearch } from "../../lib/tasks/tasks";
import { useMemberNames } from "../inbox/presence";
import { TopicPill, useTopicMap } from "../inbox/topics";
import { TaskPriorityMark, TaskStatusBadge } from "./task-bits";

type TaskListProps = {
  search: TasksSearch;
  selectedId?: string | undefined;
  className?: string;
};

/** The left column of the tasks section: views, the queue and topic filters, search and the list. */
export function TaskList({ search, selectedId, className }: TaskListProps) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const view = search.view ?? "mine";
  const { data: mine } = useTaskSummary();
  const { data: queues } = useTaskQueues();
  const { data: topics } = useTopics();
  const queueId = useId();
  const topicId = useId();
  const searchId = useId();
  const [query, setQuery] = useState(search.q ?? "");
  // The search goes to the URL after a pause in typing, so every letter is not a request.
  useEffect(() => {
    const q = query.trim();
    if (q === (search.q ?? "")) return;
    const timer = setTimeout(() => void navigate({ to: ".", search: (s: TasksSearch) => ({ ...s, q: q || undefined }) }), 300);
    return () => clearTimeout(timer);
  }, [query, search.q, navigate]);

  return (
    <section aria-label={t("tasks.listLabel")} className={cn("flex min-h-0 min-w-0 flex-col border-r border-line bg-surface", className)}>
      <div className="flex items-center gap-2 px-3 pt-3">
        <h1 className="m-0 mr-auto font-display text-base font-semibold">{t("pages.tasks.title")}</h1>
        <Button asChild size="sm" variant="primary">
          <Link to="/tasks/new" search={search}>
            <Plus aria-hidden="true" className="size-4" />
            {t("tasks.new")}
          </Link>
        </Button>
      </div>
      <nav aria-label={t("tasks.views.label")} className="flex gap-0.5 overflow-x-auto border-b border-line px-3 pt-2">
        {TASK_VIEWS.map((id) => (
          <Link
            key={id}
            to="/tasks"
            search={{ ...search, view: id }}
            aria-current={id === view ? "page" : undefined}
            className={cn(
              "flex items-center gap-1.5 border-b-2 border-transparent px-2 pt-1.5 pb-2 text-[13px] whitespace-nowrap text-ink-2 no-underline",
              "hover:text-ink aria-[current=page]:border-ink aria-[current=page]:font-medium aria-[current=page]:text-ink",
            )}
          >
            {t(`tasks.views.${id}`)}
            {id === "mine" && mine !== undefined && " "}
            {id === "mine" && mine !== undefined && <b className="font-mono text-[11px] font-medium text-lamp-ink tabular-nums">{mine}</b>}
          </Link>
        ))}
      </nav>
      <div className="flex flex-col gap-2 border-b border-line-2 p-3">
        <div className="relative">
          <label htmlFor={searchId} className="sr-only">
            {t("tasks.search")}
          </label>
          <Search aria-hidden="true" className="pointer-events-none absolute top-2.5 left-2.5 size-4 text-ink-3" />
          <input
            id={searchId}
            type="search"
            value={query}
            maxLength={100}
            placeholder={t("tasks.searchPlaceholder")}
            onChange={(e) => setQuery(e.currentTarget.value)}
            className="h-9 w-full min-w-0 rounded-lg border border-input bg-surface pr-2.5 pl-8 text-sm text-ink focus-visible:border-ring focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-ring"
          />
        </div>
        {(queues?.length ?? 0) > 0 && (
          <div>
            <label htmlFor={queueId} className="sr-only">
              {t("tasks.queueFilter")}
            </label>
            <Select
              id={queueId}
              value={search.queue ?? ""}
              onChange={(e) => {
                const queue = e.currentTarget.value || undefined;
                void navigate({ to: ".", search: (s: TasksSearch) => ({ ...s, queue }) });
              }}
            >
              <option value="">{t("tasks.allQueues")}</option>
              {queues?.map((q) => (
                <option key={q.id} value={q.id}>
                  {q.name}
                </option>
              ))}
              <option value="none">{t("tasks.noQueue")}</option>
            </Select>
          </div>
        )}
        {(topics?.length ?? 0) > 0 && (
          <div>
            <label htmlFor={topicId} className="sr-only">
              {t("tasks.topicFilter")}
            </label>
            <Select
              id={topicId}
              value={search.topic ?? ""}
              onChange={(e) => {
                const topic = e.currentTarget.value || undefined;
                void navigate({ to: ".", search: (s: TasksSearch) => ({ ...s, topic }) });
              }}
            >
              <option value="">{t("tasks.allTopics")}</option>
              {topics?.map((topic) => (
                <option key={topic.id} value={topic.id}>
                  {topic.name}
                </option>
              ))}
              <option value="none">{t("inbox.topic.without")}</option>
            </Select>
          </div>
        )}
      </div>
      <ListBody search={search} selectedId={selectedId} />
    </section>
  );
}

function ListBody({ search, selectedId }: { search: TasksSearch; selectedId?: string | undefined }) {
  const { t } = useTranslation();
  const list = useTasks(taskFilter(search));
  if (list.isPending) {
    return (
      <div className="flex flex-col gap-3 p-3" aria-busy="true" aria-label={t("app.loading")}>
        {[0, 1, 2, 3].map((i) => (
          <Skeleton key={i} className="h-12" />
        ))}
      </div>
    );
  }
  if (list.isError) {
    return (
      <div role="alert" className="flex flex-col items-start gap-2 p-4 text-sm text-ink-2">
        {t("tasks.loadError")}
        <Button size="sm" onClick={() => void list.refetch()}>
          {t("inbox.retry")}
        </Button>
      </div>
    );
  }
  const items = list.data.pages.flatMap((p) => p.items);
  if (items.length === 0) {
    return (
      <div className="flex flex-col gap-1 px-4 py-8 text-center">
        <p className="m-0 font-medium text-ink">{t(search.q ? "tasks.empty.search" : `tasks.empty.${search.view ?? "mine"}`)}</p>
        <p className="m-0 text-[13px] text-ink-3">{t("tasks.empty.hint")}</p>
      </div>
    );
  }
  return (
    <div className="min-h-0 flex-1 overflow-auto">
      <ul className="m-0 list-none p-0">
        {items.map((task) => (
          <li key={task.id}>
            <TaskRow task={task} search={search} selected={task.id === selectedId} />
          </li>
        ))}
      </ul>
      {list.hasNextPage && (
        <div className="p-3">
          <Button variant="ghost" className="w-full" disabled={list.isFetchingNextPage} onClick={() => void list.fetchNextPage()}>
            {t("inbox.loadMore")}
          </Button>
        </div>
      )}
    </div>
  );
}

function TaskRow({ task, search, selected }: { task: Task; search: TasksSearch; selected: boolean }) {
  const { t } = useTranslation();
  const names = useMemberNames();
  const topics = useTopicMap();
  const { data: queues } = useTaskQueues();
  const queue = task.queue_id ? queues?.find((q) => q.id === task.queue_id)?.name : undefined;
  const topic = task.topic_id ? topics.get(task.topic_id) : undefined;
  const assignee = task.assignee_id ? (names.get(task.assignee_id) ?? t("inbox.colleague")) : t("tasks.unassigned");
  return (
    <Link
      to="/tasks/$taskId"
      params={{ taskId: task.id }}
      search={search}
      aria-current={selected ? "page" : undefined}
      className="flex flex-col gap-1 border-b border-line-2 px-4 py-2.5 text-ink no-underline hover:bg-hover aria-[current=page]:bg-sel"
    >
      <span className="flex min-w-0 items-baseline gap-2">
        <span className="shrink-0 font-mono text-[12px] text-ink-3 tabular-nums">{t("tasks.number", { number: task.number })}</span>
        <span className="min-w-0 truncate text-[13.5px] font-medium">{task.title}</span>
      </span>
      <span className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-[12px] text-ink-2">
        <TaskStatusBadge status={task.status} />
        <TaskPriorityMark priority={task.priority} />
        <span className="min-w-0 truncate">{assignee}</span>
        {queue && <span className="min-w-0 truncate text-ink-3">· {queue}</span>}
        {topic && <TopicPill topic={topic} />}
      </span>
    </Link>
  );
}
