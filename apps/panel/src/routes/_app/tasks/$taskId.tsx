import { createFileRoute } from "@tanstack/react-router";

import { TaskView } from "../../../components/tasks/task-view";

export const Route = createFileRoute("/_app/tasks/$taskId")({
  component: TaskPage,
});

function TaskPage() {
  const { taskId } = Route.useParams();
  const search = Route.useSearch();
  return <TaskView key={taskId} taskId={taskId} search={search} />;
}
