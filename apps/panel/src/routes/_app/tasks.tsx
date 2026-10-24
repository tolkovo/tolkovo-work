import { Outlet, createFileRoute, useMatchRoute, useNavigate, useParams } from "@tanstack/react-router";
import { X } from "lucide-react";
import { Suspense, lazy, useEffect } from "react";
import { useTranslation } from "react-i18next";

import { TaskBoard } from "../../components/tasks/task-board";
import { TaskList } from "../../components/tasks/task-list";
import { readStoredTasksLayout } from "../../lib/preferences";
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
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      // Escape inside a menu or a dialog closes that first.
      if (e.key !== "Escape" || e.defaultPrevented || (e.target as HTMLElement).closest('[role="menu"], [role="dialog"]')) return;
      void navigate({ to: "/tasks", search });
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [open, navigate, search]);
  return (
    <div className="relative flex h-full min-h-0 min-w-0 overflow-clip">
      <TaskBoard search={search} openId={taskId} />
      {open && (
        <aside
          aria-label={t("pages.tasks.title")}
          className="absolute inset-y-0 right-0 z-30 flex w-[min(560px,100%)] flex-col border-l border-line bg-paper shadow-pop"
        >
          <div className="flex justify-end border-b border-line bg-surface px-2 py-1 max-nav:hidden">
            <button
              type="button"
              onClick={() => void navigate({ to: "/tasks", search })}
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
