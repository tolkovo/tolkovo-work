import type { Task, TaskBoardFilter, TaskStatus } from "@tolkovo/api-client";
import { useTaskColumnMore } from "@tolkovo/api-client/react";
import { Link } from "@tanstack/react-router";
import { Button, cn } from "@tolkovo/ui";
import { useId, useState, type DragEvent, type ReactNode } from "react";
import { useTranslation } from "react-i18next";

type ColumnProps = {
  status: TaskStatus;
  count: number;
  items: Task[];
  nextCursor: string | null;
  filter: TaskBoardFilter;
  className?: string | undefined;
  dropHint: "next" | "here" | null;
  over: boolean;
  onOver: (e: DragEvent) => void;
  onDrop: (e: DragEvent) => void;
  renderCard: (task: Task) => ReactNode;
};

/** A column: its name, count and rule, the urgent cards apart, then the rest and "show more". */
export function Column({ status, count, items, nextCursor, filter, className, dropHint, over, onOver, onDrop, renderCard }: ColumnProps) {
  const { t } = useTranslation();
  const headingId = useId();
  const more = useTaskColumnMore(filter);
  // Cards loaded below the first ones belong to that board: a fresh board drops them.
  const [loaded, setLoaded] = useState<{ base: Task[]; items: Task[]; cursor: string | null } | null>(null);
  const extra = loaded?.base === items ? loaded : null;
  const all = extra ? [...items, ...extra.items.filter((x) => !items.some((y) => y.id === x.id))] : items;
  const cursor = extra ? extra.cursor : nextCursor;
  const closed = status === "done" || status === "canceled";
  const urgent = closed ? [] : all.filter((x) => x.priority === "urgent");
  const rest = closed ? all : all.filter((x) => x.priority !== "urgent");
  return (
    <section
      aria-labelledby={headingId}
      data-status={status}
      onDragOver={onOver}
      onDrop={onDrop}
      className={cn(
        "flex min-h-0 flex-col rounded-xl border border-transparent bg-sunk max-nav:bg-transparent",
        over && "border-teal bg-teal-soft/60",
        className,
      )}
    >
      <div className="sticky top-0 z-1 rounded-t-xl bg-inherit px-3 pt-2.5 pb-2 max-nav:static max-nav:px-0.5">
        <div className="flex items-center gap-2 max-nav:hidden">
          <h2 id={headingId} className="m-0 text-[13.5px] font-semibold">
            {t(`tasks.board.column.${status}`)}
            {status === "done" && <span className="font-normal text-ink-3"> · {t("tasks.board.closedDays", { days: 14 })}</span>}
          </h2>
          <span className="rounded-md bg-surface px-1.5 py-0.5 font-mono text-[12px] text-ink-3 tabular-nums">{count}</span>
        </div>
        <p className="m-0 mt-0.5 text-[12px] text-ink-3">{t(`tasks.board.rule.${status}`)}</p>
        {dropHint && <p className="m-0 mt-1.5 text-[12px] font-medium text-teal">{t(dropHint === "next" ? "tasks.board.dropNext" : "tasks.board.dropHere")}</p>}
      </div>
      <div className="flex flex-col gap-2 px-2 pt-0.5 pb-2.5 max-nav:px-0">
        {urgent.length > 0 && <Divider label={t("tasks.board.urgent")} tone="lamp" />}
        {urgent.map(renderCard)}
        {urgent.length > 0 && rest.length > 0 && <Divider label={t("tasks.board.rest")} />}
        {rest.map(renderCard)}
        {all.length === 0 && <p className="m-0 px-2 py-4 text-center text-[12.5px] text-ink-3">{t(`tasks.board.emptyColumn.${status}`)}</p>}
        {cursor && (
          <Button
            variant="ghost"
            size="sm"
            disabled={more.isPending}
            onClick={() =>
              more.mutate(
                { status, cursor },
                { onSuccess: (page) => setLoaded({ base: items, items: [...(extra?.items ?? []), ...page.items], cursor: page.next_cursor }) },
              )
            }
          >
            {t("tasks.board.more", { count: count - all.length })}
          </Button>
        )}
        {status === "done" && !cursor && (
          <Link to="/tasks" search={{ layout: "list", view: "closed" }} className="px-2 py-1 text-center text-[12.5px] text-ink-2">
            {t("tasks.board.older")}
          </Link>
        )}
      </div>
    </section>
  );
}

function Divider({ label, tone }: { label: string; tone?: "lamp" }) {
  return (
    <p
      className={cn(
        "m-0 flex items-center gap-2 px-0.5 font-mono text-[10.5px] tracking-[.08em] uppercase after:h-px after:flex-1",
        tone === "lamp" ? "text-lamp-ink after:bg-lamp/40" : "text-ink-3 after:bg-line",
      )}
    >
      {label}
    </p>
  );
}
