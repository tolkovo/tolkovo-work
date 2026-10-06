import { createFileRoute } from "@tanstack/react-router";
import { ListTodo } from "lucide-react";
import { useTranslation } from "react-i18next";

export const Route = createFileRoute("/_app/tasks/")({
  component: PickTask,
});

/** The center of the section before a task is open. */
function PickTask() {
  const { t } = useTranslation();
  return (
    <div className="m-auto flex max-w-[380px] flex-col items-center gap-2 p-6 text-center">
      <span className="grid size-12 place-items-center rounded-full bg-sunk text-ink-2">
        <ListTodo aria-hidden="true" className="size-6" strokeWidth={1.7} />
      </span>
      <h2 className="m-0 text-base font-semibold">{t("tasks.pick.title")}</h2>
      <p className="m-0 text-ink-2">{t("tasks.pick.text")}</p>
    </div>
  );
}
