import { Link, Outlet, createFileRoute, useMatchRoute, useNavigate, useParams } from "@tanstack/react-router";
import { Maximize2, Minimize2, X } from "lucide-react";
import {
  Suspense,
  lazy,
  useEffect,
  useMemo,
  useRef,
  useState,
  type CSSProperties,
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
  TASK_PANEL_MIN_WIDTH,
  readStoredTaskPanelWidth,
  readStoredTasksLayout,
  storeTaskPanelWidth,
} from "../../lib/preferences";
import { validateTasksSearch, withoutFull, type TasksSearch } from "../../lib/tasks/tasks";

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
  const all = Route.useSearch();
  // Links and filters never carry `full`: it belongs to the task open over the board only.
  const search = useMemo(() => withoutFull(all), [all]);
  const { taskId } = useParams({ strict: false });
  const matchRoute = useMatchRoute();
  const open = Boolean(taskId) || Boolean(matchRoute({ to: "/tasks/new" }));
  const layout = search.layout ?? readStoredTasksLayout();
  if (layout === "board") return <BoardLayout search={search} full={all.full === true} open={open} taskId={taskId} />;
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

function BoardLayout({
  search: board,
  full,
  open,
  taskId,
}: {
  search: TasksSearch;
  full: boolean;
  open: boolean;
  taskId: string | undefined;
}) {
  const { t } = useTranslation();
  const navigate = useNavigate();
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
          style={fullPage ? undefined : panelWidthVars(width)}
          className={cn(
            "absolute inset-y-0 right-0 z-30 flex flex-col bg-paper",
            fullPage
              ? "left-0"
              : // A width stored on a wider window still leaves the board its strip when the window narrows.
                "w-[min(var(--panel-width),max(var(--panel-min),100%_-_var(--board-min)),100%)] border-l border-line shadow-pop max-nav:w-[min(560px,100%)]",
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

/** The board keeps at least this much of itself visible beside the panel. */
const BOARD_MIN_WIDTH = 240;
const PANEL_KEY_STEP = 40;

/** The panel's width for its CSS, which also caps it by the board's current width. */
function panelWidthVars(width: number): CSSProperties {
  return {
    "--panel-width": `${width}px`,
    "--panel-min": `${TASK_PANEL_MIN_WIDTH}px`,
    "--board-min": `${BOARD_MIN_WIDTH}px`,
  } as CSSProperties;
}

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
  // A drag still under way when the handle goes (the task closes, opens as a page) ends here.
  const endDrag = useRef<(() => void) | null>(null);
  useEffect(() => () => endDrag.current?.(), []);
  // The board's width now, so the limits follow the window as it narrows or widens; the
  // observer reports the first size as soon as it starts.
  const [frameWidth, setFrameWidth] = useState(() => window.innerWidth);
  useEffect(() => {
    const el = frame.current;
    if (!el || typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(([entry]) => entry && setFrameWidth(Math.round(entry.contentRect.width)));
    ro.observe(el);
    return () => ro.disconnect();
  }, [frame]);
  const max = Math.max(TASK_PANEL_MIN_WIDTH, frameWidth - BOARD_MIN_WIDTH);
  const clamp = (w: number) => Math.round(Math.min(max, Math.max(TASK_PANEL_MIN_WIDTH, w)));
  // What the panel shows: a width stored on a wider window is capped by CSS, and the handle says the same.
  const shown = clamp(width);
  const set = (w: number) => {
    const next = clamp(w);
    onWidth(next);
    storeTaskPanelWidth(next);
  };
  const onPointerDown = (e: ReactPointerEvent<HTMLDivElement>) => {
    if (e.button !== 0) return;
    e.preventDefault();
    const handle = e.currentTarget;
    handle.setPointerCapture(e.pointerId);
    const right = frame.current?.getBoundingClientRect().right ?? window.innerWidth;
    let last = shown;
    const stop = new AbortController();
    const end = () => {
      stop.abort();
      endDrag.current = null;
      storeTaskPanelWidth(last);
    };
    handle.addEventListener(
      "pointermove",
      (ev: PointerEvent) => {
        last = clamp(right - ev.clientX);
        onWidth(last);
      },
      { signal: stop.signal },
    );
    handle.addEventListener("pointerup", end, { signal: stop.signal });
    handle.addEventListener("pointercancel", end, { signal: stop.signal });
    endDrag.current = end;
  };
  const onKeyDown = (e: ReactKeyboardEvent<HTMLDivElement>) => {
    // The panel grows to the left, so the left arrow widens it.
    if (e.key === "ArrowLeft") set(shown + PANEL_KEY_STEP);
    else if (e.key === "ArrowRight") set(shown - PANEL_KEY_STEP);
    else if (e.key === "Home") set(max);
    else if (e.key === "End") set(TASK_PANEL_MIN_WIDTH);
    else return;
    e.preventDefault();
  };
  return (
    <div
      role="separator"
      aria-orientation="vertical"
      aria-label={t("tasks.board.resize")}
      aria-valuenow={shown}
      aria-valuemin={TASK_PANEL_MIN_WIDTH}
      aria-valuemax={max}
      tabIndex={0}
      onPointerDown={onPointerDown}
      onKeyDown={onKeyDown}
      onDoubleClick={() => set(TASK_PANEL_DEFAULT_WIDTH)}
      title={t("tasks.board.resize")}
      className="group absolute inset-y-0 -left-1 z-10 w-2 cursor-col-resize touch-none outline-none max-nav:hidden"
    >
      <span className="absolute inset-y-0 left-[3px] w-0.5 bg-transparent transition-colors group-hover:bg-teal group-focus-visible:bg-teal group-active:bg-teal" />
    </div>
  );
}
