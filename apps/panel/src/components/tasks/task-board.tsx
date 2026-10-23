import type { Task, TaskBoard as Board, TaskStatus, TaskUpdate } from "@tolkovo/api-client";
import { useMoveTask, useSession, useTaskBoard, useTaskQueues } from "@tolkovo/api-client/react";
import { Link } from "@tanstack/react-router";
import { Button, cn } from "@tolkovo/ui";
import { ChevronDown, Plus } from "lucide-react";
import { Fragment, useEffect, useMemo, useRef, useState, type DragEvent, type KeyboardEvent } from "react";
import { useTranslation } from "react-i18next";

import { describeApiError } from "../../lib/api-errors";
import { boardFilter, comparePriorities, lanesOf, neighbourStatus, nextStep, placeTask, type Lane } from "../../lib/tasks/board";
import { taskUpdate, type TaskLanes, type TasksSearch } from "../../lib/tasks/tasks";
import { useMemberNames } from "../inbox/presence";
import { Column, ColumnHeader } from "./task-board-column";
import { AssigneeFilter, BoardBody, ConfirmDone, LanesFilter } from "./task-board-parts";
import { BlockDialog } from "./task-block";
import { TaskCard } from "./task-card";
import { LayoutSwitch, TaskFilters } from "./task-filters";

type Move = { task: Task; status: TaskStatus };

/**
 * The board of the tasks section (ADR-0019): a column by status. A card moves by dragging, by
 * its menu or by Shift+←/→; the move shows at once and goes back when the server refuses it.
 * Moving a task with waiting conversations to done asks first. Columns count against their soft
 * limits; swimlanes group the cards by assignee, queue or priority. Below 760 px the columns are
 * tabs.
 */
export function TaskBoard({ search, openId }: { search: TasksSearch; openId: string | undefined }) {
  const { t } = useTranslation();
  const { data: session } = useSession();
  const me = session?.user.id;
  const filter = useMemo(() => boardFilter(search), [search]);
  const board = useTaskBoard(filter);
  const move = useMoveTask(filter, (b, task, body) => placeTask(b, task, body, me, new Date()));
  const laneName = useLaneName(search.lanes);
  const [current, setCurrent] = useState<string | undefined>(openId);
  const [confirm, setConfirm] = useState<Move | null>(null);
  const [blocking, setBlocking] = useState<Task | null>(null);
  const [folded, setFolded] = useState<ReadonlySet<string>>(new Set());
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

  const change = (task: Task, body: TaskUpdate, done: string) => {
    setError("");
    move.mutate({ task, body }, { onSuccess: () => setSaid(done), onError: (err) => setError(describeApiError(t, err).message) });
  };
  const send = ({ task, status }: Move) =>
    change(task, taskUpdate(task, { status }), t("tasks.board.moved", { number: number(task), column: columnName(status) }));
  const requestMove = (task: Task, status: TaskStatus) => {
    if (task.status === status) return;
    if (status === "done" && task.conversation_count > 0) setConfirm({ task, status });
    else send({ task, status });
  };
  const take = (task: Task) => {
    if (!me) return;
    const body = taskUpdate(task, { assignee_id: me, status: task.status === "open" ? "in_progress" : task.status });
    change(task, body, t("tasks.board.taken", { number: number(task) }));
  };
  const block = (task: Task, on: boolean) => {
    if (on) setBlocking(task);
    else change(task, taskUpdate(task, { blocked_reason: "" }), t("tasks.blocked.undone", { number: number(task) }));
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

  const renderBoard = (cols: Board["columns"]) => {
    const first = cols.find((x) => x.items.length > 0)?.items[0]?.id;
    const card = (task: Task, ageDays: number | null) => (
      <TaskCard
        key={task.id}
        task={task}
        search={search}
        current={task.id === (current ?? first)}
        open={task.id === openId}
        dragging={task.id === dragged?.id}
        me={me}
        now={now}
        ageDays={ageDays}
        onMove={requestMove}
        onTake={take}
        onBlock={block}
      />
    );
    const hint = (status: TaskStatus) => (dragged && target === status ? (status === nextStep(dragged.status) ? "next" : "here") : null);
    const column = (c: Board["columns"][number], lane?: Lane) => (
      <Column
        key={c.status}
        status={c.status}
        count={lane ? c.items.filter((x) => lane.tasks.includes(x)).length : c.count}
        items={lane ? c.items.filter((x) => lane.tasks.includes(x)) : c.items}
        nextCursor={lane ? null : c.next_cursor}
        filter={filter}
        wipLimit={c.wip_limit}
        header={!lane}
        lane={lane?.key}
        className={c.status === tab ? undefined : "max-nav:hidden"}
        dropHint={hint(c.status)}
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
        renderCard={(task) => card(task, c.age_days)}
      />
    );
    const lanes = search.lanes && shown ? lanesOf(shown, search.lanes, laneName.order) : null;
    return (
      <div
        ref={root}
        aria-describedby="board-help"
        onKeyDown={onKeyDown}
        onDragStart={onDragStart}
        onDragEnd={endDrag}
        style={{ gridTemplateColumns: `repeat(${cols.length}, minmax(260px, 1fr))` }}
        className="grid min-h-0 flex-1 auto-rows-max content-start items-start gap-3 overflow-auto px-4 pt-3.5 pb-5 max-nav:block max-nav:px-3"
      >
        <p id="board-help" className="sr-only">
          {t("tasks.board.help")}
        </p>
        {!lanes && cols.map((c) => column(c))}
        {lanes &&
          cols.map((c) => (
            <div key={c.status} className={cn("rounded-xl bg-sunk px-3 pt-2.5 pb-2 max-nav:bg-transparent max-nav:px-0.5", c.status !== tab && "max-nav:hidden")}>
              <ColumnHeader status={c.status} count={c.count} wipLimit={c.wip_limit} dropHint={hint(c.status)} />
            </div>
          ))}
        {lanes?.map((lane) => {
          const name = laneName.of(lane.key);
          const isFolded = folded.has(lane.key);
          return (
            <Fragment key={lane.key}>
              <button
                type="button"
                aria-expanded={!isFolded}
                onClick={() =>
                  setFolded((prev) => {
                    const next = new Set(prev);
                    if (next.has(lane.key)) next.delete(lane.key);
                    else next.add(lane.key);
                    return next;
                  })
                }
                className="col-span-full mt-1 flex items-center gap-2 border-b border-line pb-1 text-left text-[13px] font-semibold text-ink max-nav:mt-3 max-nav:w-full"
              >
                <ChevronDown aria-hidden="true" className={cn("size-4 text-ink-3 transition-transform", isFolded && "-rotate-90")} />
                {name}
                <span className="font-mono text-[12px] font-normal text-ink-3 tabular-nums">{lane.tasks.length}</span>
              </button>
              {!isFolded && cols.map((c) => column(c, lane))}
            </Fragment>
          );
        })}
      </div>
    );
  };

  return (
    <section aria-label={t("tasks.board.label")} className="flex min-h-0 min-w-0 flex-1 flex-col">
      <header className="flex flex-wrap items-center gap-2 border-b border-line bg-surface px-4 py-2.5 max-nav:px-3">
        <h1 className="m-0 mr-1 font-display text-base font-semibold max-nav:mr-auto">{t("pages.tasks.title")}</h1>
        <LayoutSwitch search={search} layout="board" />
        {/* One row on a phone, scrolled sideways, so the columns keep the screen. */}
        <div className="contents max-nav:order-last max-nav:flex max-nav:w-full max-nav:gap-2 max-nav:overflow-x-auto">
          <AssigneeFilter search={search} />
          <LanesFilter search={search} />
          <TaskFilters search={search} compact className="contents *:w-40 *:shrink-0 *:first:w-52 *:last:w-9" />
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
                  <b className={cn("font-mono text-[11px] font-medium tabular-nums", c.wip_limit !== null && c.count > c.wip_limit ? "text-lamp-ink" : "text-ink-3")}>
                    {c.wip_limit === null ? c.count : `${c.count}/${c.wip_limit}`}
                  </b>
                </button>
              ))}
            </div>
            {renderBoard(cols)}
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
      {blocking && (
        <BlockDialog
          number={number(blocking)}
          onCancel={() => {
            focusCard(root.current, blocking.id);
            setBlocking(null);
          }}
          onSubmit={(reason) => {
            change(blocking, taskUpdate(blocking, { blocked_reason: reason }), t("tasks.blocked.done", { number: number(blocking) }));
            focusCard(root.current, blocking.id);
            setBlocking(null);
          }}
        />
      )}
    </section>
  );
}

/** Names of the lanes and their order: people and queues by name, priorities urgent first. */
function useLaneName(by: TaskLanes | undefined) {
  const { t } = useTranslation();
  const names = useMemberNames();
  const { data: queues } = useTaskQueues();
  const of = (key: string): string => {
    if (by === "priority") return t(`tasks.priority.${key as Task["priority"]}`);
    if (key === "") return t(`tasks.board.laneNone.${by === "queue" ? "queue" : "assignee"}`);
    if (by === "queue") return queues?.find((q) => q.id === key)?.name ?? t("tasks.queueDeleted");
    return names.get(key) ?? t("inbox.colleague");
  };
  const order = (a: string, b: string) => (by === "priority" ? comparePriorities(a, b) : of(a).localeCompare(of(b)));
  return { of, order };
}

function focusCard(root: HTMLElement | null, id: string) {
  root?.querySelector<HTMLElement>(`[data-card="${id}"]`)?.focus();
}

/**
 * The card the arrow keys go to: up and down in the column, on into the same column of the next
 * swimlane; left and right to the same row of the next column with cards in the lane.
 */
function neighbourCard(root: HTMLElement | null, id: string, key: string): string | undefined {
  if (!root) return undefined;
  const cells = [...root.querySelectorAll<HTMLElement>("section[data-status]")].map((el) => ({
    lane: el.dataset.lane ?? "",
    status: el.dataset.status ?? "",
    cards: [...el.querySelectorAll<HTMLElement>("[data-card]")].map((c) => c.dataset.card ?? ""),
  }));
  const at = cells.findIndex((c) => c.cards.includes(id));
  const cell = cells[at];
  if (!cell) return undefined;
  const row = cell.cards.indexOf(id);
  if (key === "ArrowDown" || key === "ArrowUp") {
    const step = key === "ArrowDown" ? 1 : -1;
    if (cell.cards[row + step]) return cell.cards[row + step];
    const below = step > 0 ? cells.slice(at + 1) : cells.slice(0, at).reverse();
    const next = below.find((c) => c.status === cell.status && c.cards.length > 0);
    return next ? (step > 0 ? next.cards[0] : next.cards[next.cards.length - 1]) : undefined;
  }
  const step = key === "ArrowRight" ? 1 : -1;
  for (let j = at + step; j >= 0 && j < cells.length && cells[j]?.lane === cell.lane; j += step) {
    const other = cells[j]?.cards ?? [];
    if (other.length > 0) return other[Math.min(row, other.length - 1)];
  }
  return undefined;
}
