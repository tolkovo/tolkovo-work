import { createFileRoute } from "@tanstack/react-router";

import { TaskView } from "../../../components/tasks/task-view";
import { withoutFull } from "../../../lib/tasks/tasks";

export const Route = createFileRoute("/_app/tasks/$taskId")({
  component: TaskPage,
});

function TaskPage() {
  const { taskId } = Route.useParams();
  // The task's links lead back to the list, where the page view of the task means nothing.
  const search = withoutFull(Route.useSearch());
  return <TaskView key={taskId} taskId={taskId} search={search} />;
}
