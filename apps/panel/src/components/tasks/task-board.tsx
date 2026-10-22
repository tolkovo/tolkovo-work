import type { Task, TaskBoard as Board, TaskStatus } from "@tolkovo/api-client";
import { useMoveTask, useSession, useTaskBoard } from "@tolkovo/api-client/react";
import { Link } from "@tanstack/react-router";
import { Button } from "@tolkovo/ui";
import { Plus } from "lucide-react";
import { useEffect, useMemo, useRef, useState, type DragEvent, type KeyboardEvent } from "react";
import { useTranslation } from "react-i18next";

import { describeApiError } from "../../lib/api-errors";
import { boardFilter, neighbourStatus, nextStep, placeTask } from "../../lib/tasks/board";
import { taskUpdate, type TasksSearch } from "../../lib/tasks/tasks";
import { Column } from "./task-board-column";
import { AssigneeFilter, BoardBody, ConfirmDone } from "./task-board-parts";
import { TaskCard } from "./task-card";
import { LayoutSwitch, TaskFilters } from "./task-filters";

type Move = { task: Task; status: TaskStatus };

/**
 * The board of the tasks section (ADR-0019): a column by status. A card moves by dragging, by
 * its menu or by Shift+←/→; the move shows at once and goes back when the server refuses it.
 * Moving a task with waiting conversations to done asks first. Below 760 px the columns are tabs.
 */
export function TaskBoard({ search, openId }: { search: TasksSearch; openId: string | undefined }) {
  const { t } = useTranslation();
  const { data: session } = useSession();
  const me = session?.user.id;
  const filter = useMemo(() => boardFilter(search), [search]);
  const board = useTaskBoard(filter);
  const move = useMoveTask(filter, (b, task, body) => placeTask(b, task, body, me, new Date()));
  const [current, setCurrent] = useState<string | undefined>(openId);
  const [confirm, setConfirm] = useState<Move | null>(null);
  const [said, setSaid] = useState("");
  const [error, setError] = useState("");
  const [tab, setTab] = useState<TaskStatus>("in_progress");
  // While a card is dragged the board stays as it was: a refresh would move cards under the pointer.
  const [frozen, setFrozen] = useState<Board | null>(null);
  const [dragged, setDragged] = useState<Task | null>(null);
  const [target, setTarget] = useState<TaskStatus | null>(null);
  const shown = frozen ?? board.data;
  const now = useMemo(() => new Date(), [shown]); // eslint-disable-line react-hooks/exhaustive-deps -- ages refresh with the data
  const root = useRef<HTMLDivElement>(null);
  const columnName = (s: TaskStatus) => t(`tasks.board.column.${s}`);
  const number = (task: Task) => t("tasks.number", { number: task.number });

  // The open task's card is the keyboard's; closing the task brings focus back to it.
  const [lastOpen, setLastOpen] = useState(openId);
  if (openId !== lastOpen) {
    setLastOpen(openId);
    if (openId) setCurrent(openId);
  }
  const wasOpen = useRef(openId);
  useEffect(() => {
    const back = wasOpen.current;
    if (back && !openId) requestAnimationFrame(() => focusCard(root.current, back));
    wasOpen.current = openId;
  }, [openId]);

  const send = ({ task, status }: Move) => {
    setError("");
    move.mutate(
      { task, body: taskUpdate(task, { status }) },
      {
        onSuccess: () => setSaid(t("tasks.board.moved", { number: number(task), column: columnName(status) })),
        onError: (err) => setError(describeApiError(t, err).message),
      },
    );
  };
  const requestMove = (task: Task, status: TaskStatus) => {
    if (task.status === status) return;
    if (status === "done" && task.conversation_count > 0) setConfirm({ task, status });
    else send({ task, status });
  };
  const take = (task: Task) => {
    if (!me) return;
    setError("");
    const body = taskUpdate(task, { assignee_id: me, status: task.status === "open" ? "in_progress" : task.status });
    move.mutate(
      { task, body },
      { onSuccess: () => setSaid(t("tasks.board.taken", { number: number(task) })), onError: (err) => setError(describeApiError(t, err).message) },
    );
  };

  const find = (id: string | undefined) => shown?.columns.flatMap((c) => c.items).find((x) => x.id === id);
  const onKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    const id = (e.target as HTMLElement).closest<HTMLElement>("[data-card]")?.dataset.card;
    const task = find(id);
    if (!task || e.altKey || e.ctrlKey || e.metaKey) return;
    const key = e.key.toLowerCase();
    if (e.shiftKey && (e.key === "ArrowRight" || e.key === "ArrowLeft")) {
      const to = neighbourStatus(task.status, e.key === "ArrowRight" ? 1 : -1);
      if (to) {
        requestMove(task, to);
        setTab(to);
      }
    } else if (!e.shiftKey && e.key.startsWith("Arrow")) {
      const next = neighbourCard(root.current, task.id, e.key);
      if (next) {
        setCurrent(next);
        focusCard(root.current, next);
      }
    } else if (key === "m" || key === "ь") {
      root.current?.querySelector<HTMLButtonElement>(`[data-task-id="${task.id}"] [aria-haspopup="menu"]`)?.click();
    } else if (key === "a" || key === "ф") {
      take(task);
    } else return;
    e.preventDefault();
  };

  const onDragStart = (e: DragEvent) => {
    const id = (e.target as HTMLElement).closest<HTMLElement>("[data-task-id]")?.dataset.taskId;
    const task = find(id);
    if (!task || !board.data) return;
    setFrozen(board.data);
    setDragged(task);
  };
  const endDrag = () => {
    setFrozen(null);
    setDragged(null);
    setTarget(null);
  };
  const dropOn = (status: TaskStatus) => {
    const task = dragged;
    endDrag();
    if (task) requestMove(task, status);
  };

  return (
    <section aria-label={t("tasks.board.label")} className="flex min-h-0 min-w-0 flex-1 flex-col">
      <header className="flex flex-wrap items-center gap-2 border-b border-line bg-surface px-4 py-2.5 max-nav:px-3">
        <h1 className="m-0 mr-1 font-display text-base font-semibold max-nav:mr-auto">{t("pages.tasks.title")}</h1>
        <LayoutSwitch search={search} layout="board" />
        {/* One row on a phone, scrolled sideways, so the columns keep the screen. */}
        <div className="contents max-nav:order-last max-nav:flex max-nav:w-full max-nav:gap-2 max-nav:overflow-x-auto">
          <AssigneeFilter search={search} />
          <TaskFilters search={search} className="contents *:w-44 *:shrink-0 *:first:w-56" />
        </div>
        <Button asChild size="sm" variant="primary" className="ml-auto max-nav:ml-0">
          <Link to="/tasks/new" search={search}>
            <Plus aria-hidden="true" className="size-4" />
            {t("tasks.new")}
          </Link>
        </Button>
      </header>
      {error && (
        <p role="alert" className="m-0 border-b border-line bg-danger-soft px-4 py-2 text-[13px] text-ink">
          {error}
        </p>
      )}
      <p className="sr-only" aria-live="polite">
        {said}
      </p>
      <BoardBody
        board={board}
        shown={shown}
        render={(cols) => (
          <>
            <div role="tablist" aria-label={t("tasks.board.label")} className="hidden gap-1 overflow-x-auto border-b border-line bg-surface px-3 pt-2 max-nav:flex">
              {cols.map((c) => (
                <button
                  key={c.status}
                  type="button"
                  role="tab"
                  aria-selected={c.status === tab}
                  onClick={() => setTab(c.status)}
                  className="flex shrink-0 items-center gap-1.5 border-b-2 border-transparent px-2 pt-1.5 pb-2 text-[13px] whitespace-nowrap text-ink-2 aria-selected:border-ink aria-selected:font-medium aria-selected:text-ink"
                >
                  {columnName(c.status)}
                  <b className="font-mono text-[11px] font-medium text-ink-3 tabular-nums">{c.count}</b>
                </button>
              ))}
            </div>
            <div
              ref={root}
              aria-describedby="board-help"
              onKeyDown={onKeyDown}
              onDragStart={onDragStart}
              onDragEnd={endDrag}
              className="grid min-h-0 flex-1 auto-cols-[minmax(260px,1fr)] grid-flow-col items-start gap-3 overflow-auto px-4 pt-3.5 pb-5 max-nav:block max-nav:px-3"
            >
              <p id="board-help" className="sr-only">
                {t("tasks.board.help")}
              </p>
              {cols.map((c) => (
                <Column
                  key={c.status}
                  status={c.status}
                  count={c.count}
                  items={c.items}
                  nextCursor={c.next_cursor}
                  filter={filter}
                  className={c.status === tab ? undefined : "max-nav:hidden"}
                  dropHint={dragged && target === c.status ? (c.status === nextStep(dragged.status) ? "next" : "here") : null}
                  over={target === c.status}
                  onOver={(e) => {
                    if (!dragged || dragged.status === c.status) return;
                    e.preventDefault();
                    e.dataTransfer.dropEffect = "move";
                    setTarget(c.status);
                  }}
                  onDrop={(e) => {
                    e.preventDefault();
                    dropOn(c.status);
                  }}
                  renderCard={(task) => (
                    <TaskCard
                      key={task.id}
                      task={task}
                      search={search}
                      current={task.id === (current ?? cols.find((x) => x.items.length > 0)?.items[0]?.id)}
                      open={task.id === openId}
                      dragging={task.id === dragged?.id}
                      me={me}
                      now={now}
                      onMove={requestMove}
                      onTake={take}
                    />
                  )}
                />
              ))}
            </div>
          </>
        )}
      />
      {confirm && (
        <ConfirmDone
          count={confirm.task.conversation_count}
          onCancel={() => {
            focusCard(root.current, confirm.task.id);
            setConfirm(null);
          }}
          onConfirm={() => {
            send(confirm);
            setConfirm(null);
          }}
        />
      )}
    </section>
  );
}

function focusCard(root: HTMLElement | null, id: string) {
  root?.querySelector<HTMLElement>(`[data-card="${id}"]`)?.focus();
}

/** The card the arrow keys go to: up and down in the column, left and right to the same row of the next column with cards. */
function neighbourCard(root: HTMLElement | null, id: string, key: string): string | undefined {
  if (!root) return undefined;
  const columns = [...root.querySelectorAll<HTMLElement>("section[data-status]")].map((col) =>
    [...col.querySelectorAll<HTMLElement>("[data-card]")].map((el) => el.dataset.card ?? ""),
  );
  const ci = columns.findIndex((col) => col.includes(id));
  if (ci < 0) return undefined;
  const col = columns[ci] ?? [];
  const row = col.indexOf(id);
  if (key === "ArrowDown") return col[Math.min(row + 1, col.length - 1)];
  if (key === "ArrowUp") return col[Math.max(row - 1, 0)];
  const step = key === "ArrowRight" ? 1 : -1;
  for (let j = ci + step; j >= 0 && j < columns.length; j += step) {
    const other = columns[j] ?? [];
    if (other.length > 0) return other[Math.min(row, other.length - 1)];
  }
  return undefined;
}
