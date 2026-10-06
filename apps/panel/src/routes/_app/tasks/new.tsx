import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { useEffect, useRef } from "react";
import { useTranslation } from "react-i18next";

import { NewTaskForm } from "../../../components/tasks/task-form";

export const Route = createFileRoute("/_app/tasks/new")({
  component: NewTaskPage,
});

/** A task by hand; a task from a conversation starts in the conversation instead. */
function NewTaskPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const search = Route.useSearch();
  const heading = useRef<HTMLHeadingElement>(null);
  useEffect(() => heading.current?.focus(), []);
  return (
    <section aria-labelledby="new-task-title" className="flex min-h-0 flex-1 flex-col gap-4 overflow-auto bg-paper px-5 py-4 max-nav:px-3">
      <h2 id="new-task-title" ref={heading} tabIndex={-1} className="m-0 text-[17px] font-semibold outline-none">
        {t("tasks.newTitle")}
      </h2>
      <NewTaskForm
        onDone={(task) => void navigate({ to: "/tasks/$taskId", params: { taskId: task.id }, search })}
        onCancel={() => void navigate({ to: "/tasks", search })}
      />
    </section>
  );
}
