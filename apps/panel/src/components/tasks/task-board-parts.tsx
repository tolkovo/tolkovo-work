import type { TaskBoard as Board } from "@tolkovo/api-client";
import type { useTaskBoard } from "@tolkovo/api-client/react";
import { useNavigate } from "@tanstack/react-router";
import { Button, Select, Skeleton } from "@tolkovo/ui";
import { SquareKanban } from "lucide-react";
import { useEffect, useId, useRef, type ReactNode } from "react";
import { useTranslation } from "react-i18next";

import { BOARD_STATUSES } from "../../lib/tasks/board";
import { TASK_LANES, type TasksSearch } from "../../lib/tasks/tasks";

/** Pieces of the board (ADR-0019) around its columns: states, the done question, the assignee filter. */

/** Loading, error and empty states of the board, then the columns. */
export function BoardBody({
  board,
  shown,
  render,
}: {
  board: ReturnType<typeof useTaskBoard>;
  shown: Board | undefined;
  render: (cols: Board["columns"]) => ReactNode;
}) {
  const { t } = useTranslation();
  if (board.isPending) {
    return (
      <div aria-busy="true" aria-label={t("app.loading")} className="grid auto-cols-[minmax(260px,1fr)] grid-flow-col gap-3 overflow-hidden p-4">
        {BOARD_STATUSES.map((s) => (
          <div key={s} className="flex flex-col gap-2 rounded-xl bg-sunk p-2.5">
            <Skeleton className="h-4 w-1/2" />
            {[0, 1, 2].map((i) => (
              <Skeleton key={i} className="h-19" />
            ))}
          </div>
        ))}
      </div>
    );
  }
  if (board.isError || !shown) {
    return (
      <div role="alert" className="m-auto flex max-w-100 flex-col items-center gap-3 p-6 text-center">
        <p className="m-0 text-ink-2">{t("tasks.board.loadError")}</p>
        <Button size="sm" onClick={() => void board.refetch()}>
          {t("inbox.retry")}
        </Button>
      </div>
    );
  }
  if (shown.columns.every((c) => c.count === 0)) {
    return (
      <div className="m-auto flex max-w-100 flex-col items-center gap-2 p-6 text-center">
        <span className="grid size-12 place-items-center rounded-xl bg-sunk text-ink-2">
          <SquareKanban aria-hidden="true" className="size-6" strokeWidth={1.7} />
        </span>
        <h2 className="m-0 text-base font-semibold">{t("tasks.board.empty")}</h2>
        <p className="m-0 text-ink-2">{t("tasks.board.emptyHint")}</p>
      </div>
    );
  }
  return <>{render(shown.columns)}</>;
}

/** Done brings the waiting conversations back to their assignees: asked once, by the column. */
export function ConfirmDone({ count, onConfirm, onCancel }: { count: number; onConfirm: () => void; onCancel: () => void }) {
  const { t } = useTranslation();
  const titleId = useId();
  const submit = useRef<HTMLButtonElement>(null);
  useEffect(() => submit.current?.focus(), []);
  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-labelledby={titleId}
      className="fixed inset-0 z-50 grid place-items-center bg-ink/35 p-4"
      onKeyDown={(e) => {
        if (e.key === "Escape") {
          e.preventDefault();
          onCancel();
        }
      }}
      onClick={(e) => {
        if (e.target === e.currentTarget) onCancel();
      }}
    >
      <div className="flex w-full max-w-105 flex-col gap-3 rounded-2xl border border-line bg-surface p-4.5 text-ink shadow-pop">
        <h2 id={titleId} className="m-0 text-[15.5px] font-semibold">
          {t("tasks.board.confirm.title")}
        </h2>
        <p className="m-0 text-[13.5px] text-ink-2">{t("tasks.board.confirm.text", { count })}</p>
        <div className="flex justify-end gap-2">
          <Button size="sm" onClick={onCancel}>
            {t("tasks.board.confirm.cancel")}
          </Button>
          <Button ref={submit} size="sm" variant="primary" onClick={onConfirm}>
            {t("tasks.board.confirm.submit")}
          </Button>
        </div>
      </div>
    </div>
  );
}

/** The list's views as the board's assignee filter: all, mine, unassigned. */
export function AssigneeFilter({ search }: { search: TasksSearch }) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const id = useId();
  const value = search.view === "mine" || search.view === "unassigned" ? search.view : "";
  return (
    <div className="w-40 min-w-0 shrink-0">
      <label htmlFor={id} className="sr-only">
        {t("tasks.board.assignee.label")}
      </label>
      <Select
        id={id}
        value={value}
        onChange={(e) => {
          const view = (e.currentTarget.value || "all") as TasksSearch["view"];
          void navigate({ to: ".", search: (s: TasksSearch) => ({ ...s, view }) });
        }}
      >
        <option value="">{t("tasks.board.assignee.all")}</option>
        <option value="mine">{t("tasks.board.assignee.mine")}</option>
        <option value="unassigned">{t("tasks.board.assignee.unassigned")}</option>
      </Select>
    </div>
  );
}

/** Swimlanes: none, or the cards grouped by assignee, queue or priority. */
export function LanesFilter({ search }: { search: TasksSearch }) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const id = useId();
  return (
    <div className="w-40 min-w-0 shrink-0">
      <label htmlFor={id} className="sr-only">
        {t("tasks.board.lanes.label")}
      </label>
      <Select
        id={id}
        value={search.lanes ?? ""}
        onChange={(e) => {
          const lanes = (e.currentTarget.value || undefined) as TasksSearch["lanes"];
          void navigate({ to: ".", search: (s: TasksSearch) => ({ ...s, lanes }) });
        }}
      >
        <option value="">{t("tasks.board.lanes.none")}</option>
        {TASK_LANES.map((x) => (
          <option key={x} value={x}>
            {t(`tasks.board.lanes.${x}`)}
          </option>
        ))}
      </Select>
    </div>
  );
}
