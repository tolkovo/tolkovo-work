import type { Task } from "@tolkovo/api-client";
import { useCan, useTaskProjects, useTaskQueues, useTasks, useTaskSummary } from "@tolkovo/api-client/react";
import { Link } from "@tanstack/react-router";
import { Button, Skeleton, cn } from "@tolkovo/ui";
import { FolderInput, Plus } from "lucide-react";
import { useRef, useState } from "react";
import { useTranslation } from "react-i18next";

import { TASK_VIEWS, taskFilter, taskRef, type TasksSearch } from "../../lib/tasks/tasks";
import { useMemberNames } from "../inbox/presence";
import { TopicPill, useTopicMap } from "../inbox/topics";
import { TaskPriorityMark, TaskStatusBadge, WaitingPlate } from "./task-bits";
import { LayoutSwitch, TaskFilters } from "./task-filters";
import { MoveDialog, moveTargets } from "./task-move";
import { ProjectSwitch, useCurrentTaskProject } from "./task-project";
import { TaskTagDots } from "./task-tags";

type TaskListProps = {
  search: TasksSearch;
  selectedId?: string | undefined;
  className?: string;
};

/** The left column of the tasks section: views, the shared filters (TaskFilters) and the list. */
export function TaskList({ search, selectedId, className }: TaskListProps) {
  const { t } = useTranslation();
  const view = search.view ?? "mine";
  const { data: summary } = useTaskSummary();
  const project = useCurrentTaskProject();
  // In a project, my open tasks there (ADR-0022).
  const mine = project ? (summary?.projects.find((p) => p.project_id === project.id)?.assigned_open ?? 0) : summary?.assigned_open;

  return (
    <section aria-label={t("tasks.listLabel")} className={cn("@container flex min-h-0 min-w-0 flex-col border-r border-line bg-surface", className)}>
      {/* Two rows, so a phone fits them too: the section and its project, then the layout and a new task. */}
      <div className="flex min-h-8 items-center gap-2 px-3 pt-3">
        <h1 className="m-0 mr-auto font-display text-base font-semibold">{t("pages.tasks.title")}</h1>
        <ProjectSwitch search={search} />
      </div>
      <div className="flex items-center gap-2 px-3 pt-2">
        <LayoutSwitch search={search} layout="list" />
        <Button asChild size="sm" variant="primary" className="ml-auto">
          <Link to="/tasks/new" search={search} title={t("tasks.new")}>
            <Plus aria-hidden="true" className="size-4" />
            {/* A narrow column keeps the plus only. */}
            <span className="@max-[24rem]:sr-only">{t("tasks.new")}</span>
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
      <TaskFilters search={search} className="flex flex-col gap-2 border-b border-line-2 p-3" />
      <ListBody search={search} selectedId={selectedId} />
    </section>
  );
}

function ListBody({ search, selectedId }: { search: TasksSearch; selectedId?: string | undefined }) {
  const { t } = useTranslation();
  const projectId = useCurrentTaskProject()?.id;
  const list = useTasks(taskFilter(search, projectId));
  const canEdit = useCan("tasks.edit");
  const canManage = useCan("tasks.manage");
  const { data: projects } = useTaskProjects();
  // Choosing tasks to move them to another project at once (ADR-0022, P4).
  const [selecting, setSelecting] = useState(false);
  const [chosen, setChosen] = useState<ReadonlySet<string>>(new Set());
  const [moving, setMoving] = useState<Task[] | null>(null);
  const moveButton = useRef<HTMLButtonElement>(null);
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
  const mayMove = canEdit && moveTargets(projects, canManage, items).length > 0;
  const picked = items.filter((x) => chosen.has(x.id));
  const stop = () => {
    setSelecting(false);
    setChosen(new Set());
  };
  const toggle = (id: string) =>
    setChosen((cur) => {
      const next = new Set(cur);
      if (!next.delete(id)) next.add(id);
      return next;
    });
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {mayMove && (
        <div className="flex min-h-10 items-center gap-2 border-b border-line-2 px-3 py-1.5 text-[12.5px] text-ink-2">
          {selecting ? (
            <>
              <span className="mr-auto tabular-nums" aria-live="polite">
                {t("tasks.select.count", { count: picked.length })}
              </span>
              <Button ref={moveButton} size="sm" variant="primary" disabled={picked.length === 0} onClick={() => setMoving(picked)}>
                <FolderInput aria-hidden="true" className="size-4" />
                {t("tasks.move.action")}
              </Button>
              <Button size="sm" variant="ghost" onClick={stop}>
                {t("tasks.select.done")}
              </Button>
            </>
          ) : (
            <Button size="sm" variant="ghost" className="ml-auto" onClick={() => setSelecting(true)}>
              {t("tasks.select.start")}
            </Button>
          )}
        </div>
      )}
      <div className="min-h-0 flex-1 overflow-auto">
        <ul className="m-0 list-none p-0">
          {items.map((task) => (
            <li key={task.id} className={selecting ? "flex items-stretch border-b border-line-2" : undefined}>
              {selecting && (
                <label className="grid w-9 shrink-0 cursor-pointer place-items-center pl-2">
                  <input
                    type="checkbox"
                    checked={chosen.has(task.id)}
                    aria-label={t("tasks.select.label", { number: taskRef(task) })}
                    onChange={() => toggle(task.id)}
                    className="size-4"
                  />
                </label>
              )}
              <TaskRow task={task} search={search} selected={task.id === selectedId} className={selecting ? "min-w-0 flex-1 border-b-0 pl-2" : undefined} />
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
      {moving && (
        <MoveDialog
          tasks={moving}
          onDone={stop}
          onClose={() => {
            setMoving(null);
            requestAnimationFrame(() => moveButton.current?.focus());
          }}
        />
      )}
    </div>
  );
}

function TaskRow({ task, search, selected, className }: { task: Task; search: TasksSearch; selected: boolean; className?: string | undefined }) {
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
      className={cn("flex flex-col gap-1 border-b border-line-2 px-4 py-2.5 text-ink no-underline hover:bg-hover aria-[current=page]:bg-sel", className)}
    >
      <span className="flex min-w-0 items-baseline gap-2">
        <span className="shrink-0 font-mono text-[12px] text-ink-3 tabular-nums">{taskRef(task)}</span>
        <span className="min-w-0 truncate text-[13.5px] font-medium">{task.title}</span>
      </span>
      <span className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-[12px] text-ink-2">
        <TaskStatusBadge status={task.status} />
        <WaitingPlate task={task} />
        <TaskPriorityMark priority={task.priority} />
        <span className="min-w-0 truncate">{assignee}</span>
        {queue && <span className="min-w-0 truncate text-ink-3">· {queue}</span>}
        {topic && <TopicPill topic={topic} />}
        <TaskTagDots ids={task.tag_ids} />
      </span>
    </Link>
  );
}
