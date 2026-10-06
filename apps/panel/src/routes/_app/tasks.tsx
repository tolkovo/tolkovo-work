import { Outlet, createFileRoute, useMatchRoute, useParams } from "@tanstack/react-router";

import { TaskList } from "../../components/tasks/task-list";
import { validateTasksSearch } from "../../lib/tasks/tasks";

export const Route = createFileRoute("/_app/tasks")({
  validateSearch: validateTasksSearch,
  component: TasksLayout,
});

/**
 * The tasks section (ADR-0015): the list and the open task side by side, like the inbox; below
 * 760 px they take turns.
 */
function TasksLayout() {
  const search = Route.useSearch();
  const { taskId } = useParams({ strict: false });
  const matchRoute = useMatchRoute();
  const open = Boolean(taskId) || Boolean(matchRoute({ to: "/tasks/new" }));
  return (
    <div className="grid h-full grid-cols-[360px_minmax(0,1fr)] max-nav:grid-cols-[minmax(0,1fr)]">
      <TaskList search={search} selectedId={taskId} className={open ? "max-nav:hidden" : undefined} />
      <div className={open ? "flex min-h-0 min-w-0" : "flex min-h-0 min-w-0 max-nav:hidden"}>
        <Outlet />
      </div>
    </div>
  );
}
