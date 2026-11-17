import type { Task, TaskProject } from "@tolkovo/api-client";
import { useCan, useMembers, useMoveTaskToProject, useTaskProjects, useTaskQueues } from "@tolkovo/api-client/react";
import { Button, Label, Select } from "@tolkovo/ui";
import { FolderInput } from "lucide-react";
import { useEffect, useId, useRef, useState } from "react";
import { useTranslation } from "react-i18next";

import { describeApiError } from "../../lib/api-errors";
import { taskRef } from "../../lib/tasks/tasks";
import { FormMessage } from "../form/fields";
import { projectMembers, projectQueues, projectsForNewTask } from "./task-project";

/** Projects the tasks may move to: the member's own active ones (admins: any), not the one all of them are in. */
export function moveTargets(projects: TaskProject[] | undefined, seeAll: boolean, tasks: Task[]): TaskProject[] {
  return projectsForNewTask(projects, seeAll).filter((p) => !tasks.every((x) => x.project_id === p.id));
}

/** «Перенести…» of the open task: shown while there is another project to move it to. */
export function MoveButton({ task, disabled }: { task: Task; disabled: boolean }) {
  const { t } = useTranslation();
  const canEdit = useCan("tasks.edit");
  const canManage = useCan("tasks.manage");
  const { data: projects } = useTaskProjects();
  const [asking, setAsking] = useState(false);
  const button = useRef<HTMLButtonElement>(null);
  if (!canEdit || moveTargets(projects, canManage, [task]).length === 0) return null;
  return (
    <>
      <Button ref={button} variant="ghost" size="sm" className="self-start" disabled={disabled} onClick={() => setAsking(true)}>
        <FolderInput aria-hidden="true" className="size-4" />
        {t("tasks.move.action")}
      </Button>
      {asking && (
        <MoveDialog
          tasks={[task]}
          onClose={() => {
            setAsking(false);
            requestAnimationFrame(() => button.current?.focus());
          }}
        />
      )}
    </>
  );
}

/**
 * Moves one task or several to another project (ADR-0022, P4): the queue there, and for an
 * assignee who is not in it, either unassigning or (admins) adding them to the project.
 */
export function MoveDialog({ tasks, onClose, onDone }: { tasks: Task[]; onClose: () => void; onDone?: () => void }) {
  const { t } = useTranslation();
  const canManage = useCan("tasks.manage");
  const { data: projects } = useTaskProjects();
  const { data: queues } = useTaskQueues();
  const { data: members } = useMembers();
  const move = useMoveTaskToProject();
  const targets = moveTargets(projects, canManage, tasks);
  const [projectId, setProjectId] = useState(targets[0]?.id ?? "");
  const [queueId, setQueueId] = useState("");
  const [addMembers, setAddMembers] = useState(false);
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const ids = { title: useId(), project: useId(), queue: useId(), note: useId() };
  const field = useRef<HTMLSelectElement>(null);
  useEffect(() => field.current?.focus(), []);

  const target = targets.find((p) => p.id === projectId);
  const moving = tasks.filter((x) => x.project_id !== projectId);
  const skipped = tasks.length - moving.length;
  const inTarget = new Set(projectMembers(members, target).map((m) => m.user_id));
  const outsiders = members && target ? moving.filter((x) => x.assignee_id && !inTarget.has(x.assignee_id)) : [];
  const outsiderName = outsiders.length === 1 ? members?.find((m) => m.user_id === outsiders[0]?.assignee_id)?.name : undefined;
  const one = tasks.length === 1 ? tasks[0] : undefined;

  const submit = async () => {
    if (!target || moving.length === 0) return;
    setBusy(true);
    setFailure(null);
    let failed = 0;
    let reason = "";
    for (const x of moving) {
      const outsider = x.assignee_id !== null && !inTarget.has(x.assignee_id);
      const add = outsider && addMembers && canManage;
      try {
        await move.mutateAsync({
          taskId: x.id,
          project_id: target.id,
          queue_id: queueId || null,
          assignee_id: outsider && !add ? null : x.assignee_id,
          add_assignee_to_project: add,
          version: x.version,
        });
      } catch (error) {
        failed++;
        reason ||= describeApiError(t, error).message;
      }
    }
    setBusy(false);
    if (failed > 0) {
      setFailure(t("tasks.move.failed", { count: failed, reason }));
      return;
    }
    onDone?.();
    onClose();
  };

  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-labelledby={ids.title}
      className="fixed inset-0 z-50 grid place-items-center bg-ink/35 p-4"
      onKeyDown={(e) => {
        if (e.key === "Escape") {
          e.preventDefault();
          onClose();
        }
      }}
      onClick={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <form
        noValidate
        aria-describedby={ids.note}
        className="flex w-full max-w-105 flex-col gap-3 rounded-2xl border border-line bg-surface p-4.5 text-ink shadow-pop"
        onSubmit={(e) => {
          e.preventDefault();
          void submit();
        }}
      >
        <h2 id={ids.title} className="m-0 text-[15.5px] font-semibold">
          {one ? t("tasks.move.title", { number: taskRef(one) }) : t("tasks.move.titleMany", { count: tasks.length })}
        </h2>
        {targets.length === 0 ? (
          <p className="m-0 text-[13px] text-ink-2">{t("tasks.move.noTargets")}</p>
        ) : (
          <>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor={ids.project}>{t("tasks.move.project")}</Label>
              <Select
                ref={field}
                id={ids.project}
                value={projectId}
                onChange={(e) => {
                  setProjectId(e.currentTarget.value);
                  setQueueId("");
                }}
              >
                {targets.map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.name} · {p.key}
                  </option>
                ))}
              </Select>
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor={ids.queue}>{t("tasks.move.queue")}</Label>
              <Select id={ids.queue} value={queueId} onChange={(e) => setQueueId(e.currentTarget.value)}>
                <option value="">{t("tasks.noQueue")}</option>
                {projectQueues(queues, projectId).map((q) => (
                  <option key={q.id} value={q.id}>
                    {q.name}
                  </option>
                ))}
              </Select>
            </div>
            {target && outsiders.length > 0 && (
              <fieldset className="m-0 flex flex-col gap-1.5 border-0 p-0">
                <legend className="mb-1.5 p-0 text-[13px] text-ink-2">
                  {outsiderName
                    ? t("tasks.move.outsider", { name: outsiderName, project: target.name })
                    : t("tasks.move.outsiders", { count: outsiders.length, project: target.name })}
                </legend>
                <label className="flex items-center gap-2 text-[13px]">
                  <input type="radio" name="outsider" checked={!addMembers} onChange={() => setAddMembers(false)} />
                  {t("tasks.move.unassign")}
                </label>
                {canManage && (
                  <label className="flex items-center gap-2 text-[13px]">
                    <input type="radio" name="outsider" checked={addMembers} onChange={() => setAddMembers(true)} />
                    {t("tasks.move.addMember")}
                  </label>
                )}
              </fieldset>
            )}
            {skipped > 0 && !one && <p className="m-0 text-[12.5px] text-ink-2">{t("tasks.move.skipped", { count: skipped })}</p>}
          </>
        )}
        <p id={ids.note} className="m-0 text-[12.5px] text-ink-3">
          {t("tasks.move.note")}
        </p>
        <FormMessage tone="error" message={failure} />
        <div className="flex justify-end gap-2">
          <Button type="button" size="sm" onClick={onClose}>
            {t("tasks.cancel")}
          </Button>
          <Button type="submit" size="sm" variant="primary" disabled={!target || moving.length === 0 || busy}>
            {t("tasks.move.submit")}
          </Button>
        </div>
      </form>
    </div>
  );
}
