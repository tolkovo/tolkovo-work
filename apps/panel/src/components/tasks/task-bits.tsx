import type { Task, TaskPriority, TaskStatus } from "@tolkovo/api-client";
import { cn } from "@tolkovo/ui";
import { Hourglass } from "lucide-react";
import { useTranslation } from "react-i18next";

/** Small pieces shared by the task list, the task page and the forms (ADR-0015). */

const STATUS_CLASS: Record<TaskStatus, string> = {
  open: "bg-lamp-soft text-ink",
  in_progress: "bg-teal-soft text-ink",
  review: "bg-ai-soft text-ai-ink",
  verified: "bg-teal-soft text-teal-ink",
  done: "bg-sunk text-ink-2",
  canceled: "bg-sunk text-ink-3 line-through",
};

export function TaskStatusBadge({ status }: { status: TaskStatus }) {
  const { t } = useTranslation();
  return (
    <span className={cn("shrink-0 rounded-sm px-1.5 py-0.5 text-[11px] font-medium whitespace-nowrap", STATUS_CLASS[status])}>
      {t(`tasks.status.${status}`)}
    </span>
  );
}

/**
 * Priority as words for high and urgent only: normal and low stay quiet in lists, so the
 * marked ones stand out. Color is never alone, the word is there.
 */
export function TaskPriorityMark({ priority, quietNormal = true }: { priority: TaskPriority; quietNormal?: boolean }) {
  const { t } = useTranslation();
  if (quietNormal && (priority === "normal" || priority === "low")) return null;
  return (
    <span
      className={cn(
        "shrink-0 text-[11px] font-medium whitespace-nowrap",
        priority === "urgent" ? "text-danger" : priority === "high" ? "text-lamp-ink" : "text-ink-3",
      )}
    >
      {t(`tasks.priority.${priority}`)}
    </span>
  );
}

/** Whether an open task waits for an open task that blocks it (ADR-0022). */
export function isWaiting(task: Pick<Task, "status" | "waiting_on">): boolean {
  return task.waiting_on.length > 0 && task.status !== "done" && task.status !== "canceled";
}

/**
 * The amber «ждёт OPS-3» of a task that waits: the first task it waits for, and how many more. A
 * task the member does not see is named only as a task.
 */
export function WaitingPlate({ task, className }: { task: Pick<Task, "status" | "waiting_on">; className?: string }) {
  const { t } = useTranslation();
  if (!isWaiting(task)) return null;
  const [first] = task.waiting_on;
  const more = task.waiting_on.length - 1;
  const refs = task.waiting_on.map((b) => b.ref ?? t("tasks.waiting.hidden")).join(", ");
  return (
    <span
      title={t("tasks.waiting.title", { refs })}
      className={cn(
        "inline-flex shrink-0 items-center gap-1 rounded-sm bg-lamp-soft px-1.5 py-px text-[11px] font-medium whitespace-nowrap text-lamp-ink",
        className,
      )}
    >
      <Hourglass aria-hidden="true" className="size-3" />
      {first?.ref ? t("tasks.waiting.on", { ref: first.ref }) : t("tasks.waiting.onHidden")}
      {more > 0 && <span className="font-mono tabular-nums">+{more}</span>}
      <span className="sr-only">{t("tasks.waiting.title", { refs })}</span>
    </span>
  );
}

/** Text areas of the task forms, styled like the inputs. */
export const control = cn(
  "w-full min-w-0 rounded-lg border border-input bg-surface px-2.5 text-sm text-ink",
  "focus-visible:border-ring focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-ring",
  "disabled:cursor-not-allowed disabled:opacity-55",
);
