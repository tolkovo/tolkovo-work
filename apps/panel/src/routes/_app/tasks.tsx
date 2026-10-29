import { Link, Outlet, createFileRoute, useMatchRoute, useNavigate, useParams } from "@tanstack/react-router";
import { Maximize2, Minimize2, X } from "lucide-react";
import {
  Suspense,
  lazy,
  useEffect,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent as ReactKeyboardEvent,
  type PointerEvent as ReactPointerEvent,
  type RefObject,
} from "react";
import { cn } from "@tolkovo/ui";
import { useTranslation } from "react-i18next";

import { TaskBoard } from "../../components/tasks/task-board";
import { TaskList } from "../../components/tasks/task-list";
import {
  TASK_PANEL_DEFAULT_WIDTH,
  readStoredTaskPanelWidth,
  readStoredTasksLayout,
  storeTaskPanelWidth,
} from "../../lib/preferences";
import { validateTasksSearch } from "../../lib/tasks/tasks";

// The flow metrics load with their tab: most visits never open them.
const TaskFlowView = lazy(() => import("../../components/tasks/task-flow"));

export const Route = createFileRoute("/_app/tasks")({
  validateSearch: validateTasksSearch,
  component: TasksLayout,
});

/**
 * The tasks section (ADR-0015): the list and the open task side by side, like the inbox; below
 * 760 px they take turns. As the board (ADR-0019) the columns take the width and the open task
 * slides over them from the right.
 */
function TasksLayout() {
  const search = Route.useSearch();
  const { taskId } = useParams({ strict: false });
  const matchRoute = useMatchRoute();
  const open = Boolean(taskId) || Boolean(matchRoute({ to: "/tasks/new" }));
  const layout = search.layout ?? readStoredTasksLayout();
  if (layout === "board") return <BoardLayout open={open} taskId={taskId} />;
  if (layout === "flow")
    return (
      <div className="flex h-full min-h-0 min-w-0">
        <Suspense fallback={null}>
          <TaskFlowView search={search} />
        </Suspense>
      </div>
    );
  return (
    <div className="grid h-full grid-cols-[360px_minmax(0,1fr)] max-nav:grid-cols-[minmax(0,1fr)]">
      <TaskList search={search} selectedId={taskId} className={open ? "max-nav:hidden" : undefined} />
      <div className={open ? "flex min-h-0 min-w-0" : "flex min-h-0 min-w-0 max-nav:hidden"}>
        <Outlet />
      </div>
    </div>
  );
}

function BoardLayout({ open, taskId }: { open: boolean; taskId: string | undefined }) {
  const { t } = useTranslation();
  const search = Route.useSearch();
  const navigate = useNavigate();
  const full = search.full === true;
  // The board and its links never carry `full`: it belongs to the open task only.
  const board = useMemo(() => {
    const { full: _full, ...rest } = search;
    return rest;
  }, [search]);
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      // Escape inside a menu or a dialog closes that first.
      if (e.key !== "Escape" || e.defaultPrevented || (e.target as HTMLElement).closest('[role="menu"], [role="dialog"]')) return;
      void navigate({ to: "/tasks", search: board });
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [open, navigate, board]);
  const frame = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(readStoredTaskPanelWidth);
  const fullPage = open && full;
  return (
    <div ref={frame} className="relative flex h-full min-h-0 min-w-0 overflow-clip">
      <div inert={fullPage} className={cn("flex min-h-0 min-w-0 flex-1", fullPage && "invisible")}>
        <TaskBoard search={board} openId={taskId} />
      </div>
      {open && (
        <aside
          aria-label={t("pages.tasks.title")}
          style={fullPage ? undefined : { width: `min(${width}px, 100%)` }}
          className={cn(
            "absolute inset-y-0 right-0 z-30 flex flex-col bg-paper",
            fullPage ? "left-0" : "border-l border-line shadow-pop",
          )}
        >
          {!fullPage && <PanelResizer frame={frame} width={width} onWidth={setWidth} />}
          <div className="flex justify-end gap-1 border-b border-line bg-surface px-2 py-1 max-nav:hidden">
            {taskId && (
              <Link
                to="/tasks/$taskId"
                params={{ taskId }}
                search={fullPage ? board : { ...board, full: true }}
                className="flex h-7 items-center gap-1.5 rounded-md px-2 text-[12.5px] text-ink-2 no-underline hover:bg-hover hover:text-ink"
              >
                {fullPage ? t("tasks.board.sidePanel") : t("tasks.board.fullPage")}
                {fullPage ? (
                  <Minimize2 aria-hidden="true" className="size-4" />
                ) : (
                  <Maximize2 aria-hidden="true" className="size-4" />
                )}
              </Link>
            )}
            <button
              type="button"
              onClick={() => void navigate({ to: "/tasks", search: board })}
              className="flex h-7 items-center gap-1.5 rounded-md px-2 text-[12.5px] text-ink-2 hover:bg-hover hover:text-ink"
            >
              {t("tasks.board.close")}
              <X aria-hidden="true" className="size-4" />
            </button>
          </div>
          <div className="flex min-h-0 flex-1">
            <Outlet />
          </div>
        </aside>
      )}
    </div>
  );
}

const PANEL_MIN_WIDTH = 380;
/** The board keeps at least this much of itself visible beside the panel. */
const BOARD_MIN_WIDTH = 240;
const PANEL_KEY_STEP = 40;

/**
 * The left edge of the open task: drag it, or focus it and use the arrows, to widen the panel
 * over the board; a double click goes back to the default width. The width is remembered.
 */
function PanelResizer({
  frame,
  width,
  onWidth,
}: {
  frame: RefObject<HTMLDivElement | null>;
  width: number;
  onWidth: (width: number) => void;
}) {
  const { t } = useTranslation();
  const max = () => Math.max(PANEL_MIN_WIDTH, (frame.current?.clientWidth ?? window.innerWidth) - BOARD_MIN_WIDTH);
  const clamp = (w: number) => Math.round(Math.min(max(), Math.max(PANEL_MIN_WIDTH, w)));
  const set = (w: number, keep: boolean) => {
    const next = clamp(w);
    onWidth(next);
    if (keep) storeTaskPanelWidth(next);
  };
  const onPointerDown = (e: ReactPointerEvent<HTMLDivElement>) => {
    if (e.button !== 0) return;
    e.preventDefault();
    const handle = e.currentTarget;
    handle.setPointerCapture(e.pointerId);
    const right = frame.current?.getBoundingClientRect().right ?? window.innerWidth;
    let last = width;
    const move = (ev: PointerEvent) => {
      last = clamp(right - ev.clientX);
      onWidth(last);
    };
    const up = () => {
      handle.removeEventListener("pointermove", move);
      handle.removeEventListener("pointerup", up);
      handle.removeEventListener("pointercancel", up);
      storeTaskPanelWidth(last);
    };
    handle.addEventListener("pointermove", move);
    handle.addEventListener("pointerup", up);
    handle.addEventListener("pointercancel", up);
  };
  const onKeyDown = (e: ReactKeyboardEvent<HTMLDivElement>) => {
    // The panel grows to the left, so the left arrow widens it.
    if (e.key === "ArrowLeft") set(width + PANEL_KEY_STEP, true);
    else if (e.key === "ArrowRight") set(width - PANEL_KEY_STEP, true);
    else if (e.key === "Home") set(max(), true);
    else if (e.key === "End") set(PANEL_MIN_WIDTH, true);
    else return;
    e.preventDefault();
  };
  return (
    <div
      role="separator"
      aria-orientation="vertical"
      aria-label={t("tasks.board.resize")}
      aria-valuenow={width}
      aria-valuemin={PANEL_MIN_WIDTH}
      tabIndex={0}
      onPointerDown={onPointerDown}
      onKeyDown={onKeyDown}
      onDoubleClick={() => set(TASK_PANEL_DEFAULT_WIDTH, true)}
      title={t("tasks.board.resize")}
      className="group absolute inset-y-0 -left-1 z-10 w-2 cursor-col-resize touch-none outline-none max-nav:hidden"
    >
      <span className="absolute inset-y-0 left-[3px] w-0.5 bg-transparent transition-colors group-hover:bg-teal group-focus-visible:bg-teal group-active:bg-teal" />
    </div>
  );
}
