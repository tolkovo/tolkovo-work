import type { Member, TaskProject, TaskQueue } from "@tolkovo/api-client";
import { useCan, useTaskProjects } from "@tolkovo/api-client/react";
import { useNavigate } from "@tanstack/react-router";
import { ChevronDown } from "lucide-react";
import { createContext, useContext } from "react";
import { useTranslation } from "react-i18next";

import type { TasksSearch } from "../../lib/tasks/tasks";
import { Menu, type MenuItem } from "../menu";
import { ProjectTile } from "./project-icon";

/**
 * Projects of tasks (ADR-0022): the section shows one project, named by its key in the URL
 * (`/tasks?project=CRM`), or every project the member sees.
 */

/** The project the section shows; undefined: all of them. */
export const TaskProjectContext = createContext<TaskProject | undefined>(undefined);

export function useCurrentTaskProject(): TaskProject | undefined {
  return useContext(TaskProjectContext);
}

/** The project of a key in the URL, among the projects the member sees. */
export function projectByKey(projects: TaskProject[] | undefined, key: string | undefined): TaskProject | undefined {
  return key ? projects?.find((p) => p.key === key) : undefined;
}

/** The queues of a project; with no project known, all of them. */
export function projectQueues(queues: TaskQueue[] | undefined, projectId: string | undefined): TaskQueue[] {
  if (!queues) return [];
  return projectId ? queues.filter((q) => q.project_id === projectId) : queues;
}

/** Who can be the assignee of a project's task: anyone in a project of the team, else its members. */
export function projectMembers(members: Member[] | undefined, project: TaskProject | undefined): Member[] {
  if (!members) return [];
  if (!project || project.access === "team") return members;
  return members.filter((m) => project.member_ids.includes(m.user_id));
}

/** The project a new task goes to when none is picked: the default one. */
export function defaultProject(projects: TaskProject[] | undefined): TaskProject | undefined {
  return projects?.find((p) => p.is_default);
}

/** Projects a new task can go to: active ones, the member's own; admins (`seeAll`) any. */
export function projectsForNewTask(projects: TaskProject[] | undefined, seeAll: boolean): TaskProject[] {
  return (projects ?? []).filter((p) => p.archived_at === null && (seeAll || p.member));
}

/** The project of a task, named where the workspace has more than one. */
export function ProjectName({ projectId }: { projectId: string }) {
  const { t } = useTranslation();
  const { data: projects } = useTaskProjects();
  const project = projects?.find((p) => p.id === projectId);
  if (!project || projects === undefined || projects.length < 2) return null;
  return (
    <span className="inline-flex h-6 items-center rounded-md border border-line px-2 text-[12px] text-ink-2">
      <span className="sr-only">{t("tasks.fields.project")}: </span>
      {project.name}
    </span>
  );
}

/**
 * The project switch of the section's header: all projects, the member's own, for admins the
 * ones without them, and a new project. Hidden while the workspace has one project and nobody
 * here may add another.
 */
export function ProjectSwitch({ search }: { search: TasksSearch }) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const canManage = useCan("tasks.manage");
  const { data: projects } = useTaskProjects();
  const current = useCurrentTaskProject();
  const active = (projects ?? []).filter((p) => p.archived_at === null);
  if (active.length < 2 && !canManage) return null;
  // A project changes the queues: a queue of another project would show nothing.
  const go = (key: string | undefined) => void navigate({ to: "/tasks", search: { ...search, project: key, queue: undefined } });
  const item = (p: TaskProject, group?: string): MenuItem => ({
    label: p.name,
    detail: p.key,
    checked: current?.id === p.id,
    onSelect: () => go(p.key),
    ...(group ? { group } : {}),
  });
  const items: MenuItem[] = [
    { label: t("tasks.projects.all"), checked: current === undefined, onSelect: () => go(undefined) },
    ...active.filter((p) => p.member).map((p) => item(p)),
    ...active.filter((p) => !p.member).map((p) => item(p, t("tasks.projects.notMine"))),
  ];
  if (canManage) {
    items.push({
      label: t("tasks.projects.new"),
      group: t("tasks.projects.manage"),
      onSelect: () => void navigate({ to: "/settings/tasks", hash: "projects" }),
    });
  }
  return (
    <Menu
      align="start"
      label={t("tasks.projects.label", { name: current ? current.name : t("tasks.projects.all") })}
      items={items}
      triggerClassName="flex h-8 max-w-56 items-center gap-1.5 rounded-lg border border-line bg-surface px-2.5 text-[13px] text-ink hover:bg-hover"
      trigger={
        <>
          {current && <ProjectTile icon={current.icon} color={current.color} projectKey={current.key} size="sm" />}
          <span className="truncate font-medium">{current ? current.name : t("tasks.projects.all")}</span>
          {current && <span className="font-mono text-[11px] text-ink-3">{current.key}</span>}
          <ChevronDown aria-hidden="true" className="size-3.5 shrink-0 text-ink-3" />
        </>
      }
    />
  );
}
