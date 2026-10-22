import type { Task, TaskStatus } from "@tolkovo/api-client";
import type { TFunction } from "i18next";
import { Link } from "@tanstack/react-router";
import { cn } from "@tolkovo/ui";
import { Ellipsis, MessageCircle } from "lucide-react";
import { useTranslation } from "react-i18next";

import { BOARD_STATUSES, daysInColumn, hoursInColumn, nextStep } from "../../lib/tasks/board";
import type { TasksSearch } from "../../lib/tasks/tasks";
import { TAG_DOT } from "../inbox/palette";
import { useMemberNames } from "../inbox/presence";
import { useTopicMap } from "../inbox/topics";
import { Menu, type MenuItem } from "../menu";
import { TaskPriorityMark } from "./task-bits";
import { useTaskTagMap } from "./task-tags";

type TaskCardProps = {
  task: Task;
  search: TasksSearch;
  /** The card the keyboard is on: the only one in the tab order. */
  current: boolean;
  open: boolean;
  /** Being dragged: dimmed where it was. */
  dragging: boolean;
  me: string | undefined;
  now: Date;
  onMove: (task: Task, status: TaskStatus) => void;
  onTake: (task: Task) => void;
};

/**
 * A card of the board (ADR-0019): the whole card opens the task, the "⋯" menu moves it. The
 * card is dragged by HTML5 drag and drop; the board handles the drop and the keys.
 */
export function TaskCard({ task, search, current, open, dragging, me, now, onMove, onTake }: TaskCardProps) {
  const { t } = useTranslation();
  const names = useMemberNames();
  const topics = useTopicMap();
  const tags = useTaskTagMap();
  const number = t("tasks.number", { number: task.number });
  const closed = task.status === "done" || task.status === "canceled";
  const age = ageText(t, task, now);
  const topic = task.topic_id ? topics.get(task.topic_id) : undefined;
  const assignee = task.assignee_id ? (names.get(task.assignee_id) ?? t("inbox.colleague")) : t("tasks.unassigned");
  return (
    <div
      draggable
      data-task-id={task.id}
      onDragStart={(e) => {
        e.dataTransfer.effectAllowed = "move";
        e.dataTransfer.setData("text/plain", task.id);
      }}
      className={cn(
        "group relative flex cursor-grab flex-col gap-1.5 rounded-[10px] border border-line bg-surface py-2 pr-2.5 pl-3",
        "hover:border-ink-3 has-[a:focus-visible]:outline-2 has-[a:focus-visible]:outline-offset-1 has-[a:focus-visible]:outline-ring",
        open && "border-teal shadow-[inset_0_0_0_1px_var(--color-teal)]",
        dragging && "opacity-40",
      )}
    >
      {topic && <span aria-hidden="true" title={topic.name} className={cn("absolute inset-y-0 left-0 w-0.75 rounded-l-[10px]", TAG_DOT[topic.color])} />}
      <span className="flex min-h-5 items-center gap-1.5">
        <span className="font-mono text-[11.5px] text-ink-3 tabular-nums">{number}</span>
        <TaskPriorityMark priority={task.priority} />
        <span className="ml-auto" />
        <CardMenu task={task} me={me} onMove={onMove} onTake={onTake} />
      </span>
      <Link
        to="/tasks/$taskId"
        params={{ taskId: task.id }}
        search={search}
        draggable={false}
        tabIndex={current ? 0 : -1}
        data-card={task.id}
        aria-current={open ? "page" : undefined}
        aria-label={t("tasks.board.card", { number, title: task.title })}
        className={cn(
          "line-clamp-2 text-[13.5px] leading-snug text-ink no-underline outline-none after:absolute after:inset-0",
          closed && "text-ink-2",
        )}
      >
        {task.title}
      </Link>
      <span className="flex min-w-0 items-center gap-2 text-[12px] text-ink-3">
        <span
          title={assignee}
          className={cn(
            "grid size-5.5 shrink-0 place-items-center rounded-full text-[9.5px] font-semibold",
            task.assignee_id ? "bg-teal-soft text-teal" : "border border-dashed border-line",
          )}
        >
          {task.assignee_id ? initials(assignee) : "—"}
          <span className="sr-only">{assignee}</span>
        </span>
        {task.tag_ids.length > 0 && (
          <span className="flex gap-0.5">
            {task.tag_ids.map((id) => {
              const tag = tags.get(id);
              return tag ? <span key={id} title={tag.name} className={cn("size-2 rounded-full", TAG_DOT[tag.color])} /> : null;
            })}
          </span>
        )}
        {task.conversation_count > 0 && !closed && (
          <span
            title={t("tasks.board.conversations", { count: task.conversation_count })}
            className="inline-flex items-center gap-1 rounded-full bg-lamp-soft px-1.5 font-medium text-lamp-ink"
          >
            <MessageCircle aria-hidden="true" className="size-3" />
            {task.conversation_count}
            <span className="sr-only">{t("tasks.board.conversations", { count: task.conversation_count })}</span>
          </span>
        )}
        <span
          className="ml-auto font-mono text-[11.5px] tabular-nums"
          title={t(closed ? "tasks.board.closedAge" : "tasks.board.ageTitle", { age })}
        >
          {age}
        </span>
      </span>
    </div>
  );
}

/** "Move to…" with the next step first, then taking, opening and canceling. */
function CardMenu({ task, me, onMove, onTake }: Pick<TaskCardProps, "task" | "me" | "onMove" | "onTake">) {
  const { t } = useTranslation();
  const next = nextStep(task.status);
  const targets = BOARD_STATUSES.filter((s) => s !== task.status).sort((a, b) => Number(b === next) - Number(a === next));
  const items: MenuItem[] = [
    ...targets.map((s) => ({
      label: t("tasks.board.moveTo", { column: t(`tasks.board.column.${s}`) }),
      ...(s === next ? { detail: t("tasks.board.nextStep") } : {}),
      onSelect: () => onMove(task, s),
    })),
    { label: t("tasks.board.takeMine"), hint: "A", disabled: task.assignee_id === me || me === undefined, onSelect: () => onTake(task) },
    ...(task.status === "canceled" ? [] : [{ label: t("tasks.board.cancelTask"), onSelect: () => onMove(task, "canceled") }]),
  ];
  return (
    <Menu
      label={t("tasks.board.actions", { number: t("tasks.number", { number: task.number }) })}
      trigger={<Ellipsis aria-hidden="true" className="size-4" />}
      items={items}
      triggerClassName={cn(
        "relative z-10 -my-1 grid size-6 place-items-center rounded-md text-ink-3 opacity-0 hover:bg-hover hover:text-ink",
        "group-hover:opacity-100 group-has-[a:focus-visible]:opacity-100 focus-visible:opacity-100 aria-expanded:opacity-100 max-nav:opacity-100",
      )}
    />
  );
}

function initials(name: string): string {
  return name
    .split(/\s+/)
    .filter(Boolean)
    .slice(0, 2)
    .map((w) => w[0]?.toUpperCase() ?? "")
    .join("");
}

function ageText(t: TFunction, task: Task, now: Date): string {
  const days = daysInColumn(task, now);
  if (days > 0) return t("tasks.board.age.days", { count: days });
  const hours = hoursInColumn(task, now);
  return hours > 0 ? t("tasks.board.age.hours", { count: hours }) : t("tasks.board.age.now");
}
