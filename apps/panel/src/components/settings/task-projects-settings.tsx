import type { TagColor, TaskProject, TaskProjectInput } from "@tolkovo/api-client";
import { useCreateTaskProject, useDeleteTaskProject, useMembers, useTaskProjects, useUpdateTaskProject } from "@tolkovo/api-client/react";
import { Button, Label, Select, Skeleton, cn } from "@tolkovo/ui";
import { useId, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

import { describeApiError } from "../../lib/api-errors";
import { FormMessage, TextField } from "../form/fields";
import { TAG_DOT } from "../inbox/palette";
import { PROJECT_ICON_KEYS, PROJECT_ICONS, ProjectTile, type ProjectIconKey } from "../tasks/project-icon";

const COLORS: readonly TagColor[] = ["gray", "teal", "amber", "red", "violet", "blue", "green"];
/** At most this many projects in a workspace (MaxProjects on the server). */
const MAX_PROJECTS = 50;

/**
 * Projects of tasks (ADR-0022): each has its own key and numbers (CRM-41), and either its members
 * or the whole team see its tasks. The default project takes tasks made without one; it is never
 * archived or deleted. A project with tasks is archived instead of deleted.
 */
export function TaskProjects() {
  const { t } = useTranslation();
  const projects = useTaskProjects();
  const [adding, setAdding] = useState(false);
  return (
    <section id="projects" aria-labelledby="settings-projects-title" className="flex scroll-mt-4 flex-col gap-3">
      <div className="flex items-center gap-2">
        <h3 id="settings-projects-title" className="m-0 mr-auto text-[14px] font-semibold">
          {t("settings.tasks.projects")}
        </h3>
        {!adding && projects.isSuccess && projects.data.length < MAX_PROJECTS && (
          <Button size="sm" onClick={() => setAdding(true)}>
            {t("settings.tasks.addProject")}
          </Button>
        )}
      </div>
      <p className="m-0 text-[12.5px] text-ink-2">{t("settings.tasks.projectsHint")}</p>
      {adding && <ProjectForm onDone={() => setAdding(false)} />}
      {projects.isPending ? (
        <Skeleton className="h-16" />
      ) : projects.isError ? (
        <p role="alert" className="m-0 text-[13px] text-ink-2">
          {describeApiError(t, projects.error).message}
        </p>
      ) : (
        <ul className="m-0 flex list-none flex-col gap-2 p-0">
          {projects.data.map((p) => (
            <ProjectItem key={p.id} project={p} />
          ))}
        </ul>
      )}
    </section>
  );
}

/** The whole state of a project for PUT, with the change applied. */
function projectBody(p: TaskProject, change: Partial<TaskProjectInput> = {}): TaskProjectInput {
  return {
    name: p.name,
    icon: p.icon,
    color: p.color,
    access: p.access,
    position: p.position,
    archived: p.archived_at !== null,
    member_ids: p.member_ids,
    ...change,
  };
}

function ProjectItem({ project }: { project: TaskProject }) {
  const { t } = useTranslation();
  const { data: members } = useMembers();
  const update = useUpdateTaskProject();
  const remove = useDeleteTaskProject();
  const [editing, setEditing] = useState(false);
  const [confirm, setConfirm] = useState(false);
  if (editing) {
    return (
      <li>
        <ProjectForm project={project} onDone={() => setEditing(false)} />
      </li>
    );
  }
  const archived = project.archived_at !== null;
  const names = project.member_ids.map((id) => members?.find((m) => m.user_id === id)?.name).filter(Boolean);
  const who = project.access === "team" ? t("settings.tasks.projectTeam") : names.length > 0 ? names.join(", ") : t("settings.tasks.projectNobody");
  const error = update.error ?? remove.error;
  return (
    <li className={cn("flex flex-wrap items-center gap-2 rounded-lg border border-line bg-surface px-3 py-2 text-[13px]", archived && "bg-sunk")}>
      <span className="flex min-w-0 flex-1 flex-col gap-0.5">
        <span className="inline-flex items-center gap-1.5 font-medium">
          <ProjectTile icon={project.icon} color={project.color} projectKey={project.key} size="sm" />
          {project.name}
          <span className="font-mono text-[11.5px] font-normal text-ink-3">{project.key}</span>
          {project.is_default && <span className="text-[12px] font-normal text-ink-3">{t("settings.tasks.projectDefault")}</span>}
          {archived && <span className="text-[12px] font-normal text-ink-3">{t("settings.tasks.projectArchived")}</span>}
        </span>
        <span className="text-[12.5px] text-ink-2">
          {t("settings.tasks.openTasks", { count: project.open_tasks })}
          {" · "}
          {who}
        </span>
      </span>
      {confirm ? (
        <>
          <span className="text-ink-2">{t("settings.tasks.deleteProjectConfirm")}</span>
          <Button size="sm" variant="destructive" disabled={remove.isPending} onClick={() => remove.mutate(project.id)}>
            {t("settings.tasks.deleteProject")}
          </Button>
          <Button size="sm" onClick={() => setConfirm(false)}>
            {t("tasks.cancel")}
          </Button>
        </>
      ) : (
        <>
          {!archived && (
            <Button size="sm" variant="ghost" onClick={() => setEditing(true)}>
              {t("settings.tasks.editProject")}
            </Button>
          )}
          {!project.is_default && (
            <Button
              size="sm"
              variant="ghost"
              disabled={update.isPending}
              onClick={() => update.mutate({ id: project.id, ...projectBody(project, { archived: !archived }) })}
            >
              {archived ? t("settings.tasks.restoreProject") : t("settings.tasks.archiveProject")}
            </Button>
          )}
          {!project.is_default && (
            <Button size="sm" variant="ghost" className="text-danger" onClick={() => setConfirm(true)}>
              {t("settings.tasks.deleteProject")}
            </Button>
          )}
        </>
      )}
      {error && (
        <p role="alert" className="m-0 w-full text-danger">
          {describeApiError(t, error).message}
        </p>
      )}
    </li>
  );
}

function ProjectForm({ project, onDone }: { project?: TaskProject; onDone: () => void }) {
  const { t } = useTranslation();
  const { data: members } = useMembers();
  const create = useCreateTaskProject();
  const update = useUpdateTaskProject();
  const save = project ? update : create;
  const [name, setName] = useState(project?.name ?? "");
  const [key, setKey] = useState(project?.key ?? "");
  const [color, setColor] = useState<TagColor>(project?.color ?? "blue");
  const [icon, setIcon] = useState<ProjectIconKey | null>(project?.icon ?? null);
  const [access, setAccess] = useState<TaskProject["access"]>(project?.access ?? "members");
  const [memberIds, setMemberIds] = useState<string[]>(project?.member_ids ?? []);
  const [errors, setErrors] = useState<{ name?: string; key?: string }>({});
  const prefix = project ? `project-${project.id}` : "project-new";
  const colorId = useId();

  const submit = (e: FormEvent) => {
    e.preventDefault();
    const next: { name?: string; key?: string } = {};
    if (!name.trim()) next.name = t("settings.tasks.projectNameRequired");
    if (!project && !/^[A-Za-z]{2,6}$/.test(key)) next.key = t("settings.tasks.projectKeyInvalid");
    setErrors(next);
    if (next.name || next.key) {
      document.getElementById(next.name ? `${prefix}-name` : `${prefix}-key`)?.focus();
      return;
    }
    const change = { name: name.trim(), icon, color, access, member_ids: memberIds };
    if (project) update.mutate({ id: project.id, ...projectBody(project, change) }, { onSuccess: onDone });
    else create.mutate({ ...change, key: key.toUpperCase() }, { onSuccess: onDone });
  };

  const error = save.isError ? describeApiError(t, save.error, { project_exists: "name" }) : null;
  return (
    <form noValidate onSubmit={submit} className="flex flex-col gap-3 rounded-lg border border-teal bg-surface p-3">
      <TextField
        id={`${prefix}-name`}
        name="name"
        label={t("settings.tasks.projectName")}
        value={name}
        maxLength={60}
        error={errors.name ?? (error?.field === "name" ? error.message : undefined)}
        onChange={(e) => setName(e.currentTarget.value)}
      />
      {project ? (
        <p className="m-0 text-[13px] text-ink-2">
          {t("settings.tasks.projectKeyFixed")} <span className="font-mono text-ink">{project.key}</span>
        </p>
      ) : (
        <TextField
          id={`${prefix}-key`}
          name="key"
          label={t("settings.tasks.projectKey")}
          hint={t("settings.tasks.projectKeyHint")}
          value={key}
          maxLength={6}
          autoComplete="off"
          error={errors.key}
          onChange={(e) => setKey(e.currentTarget.value.replace(/[^A-Za-z]/g, "").toUpperCase())}
          className="font-mono uppercase"
        />
      )}
      <div className="flex flex-col gap-1.5">
        <Label htmlFor={colorId}>{t("settings.tasks.projectColor")}</Label>
        <div className="flex items-center gap-2">
          <span aria-hidden="true" className={cn("size-3 shrink-0 rounded-full", TAG_DOT[color])} />
          <Select id={colorId} value={color} onChange={(e) => setColor(e.currentTarget.value as TagColor)}>
            {COLORS.map((c) => (
              <option key={c} value={c}>
                {t(`settings.tasks.colors.${c}`)}
              </option>
            ))}
          </Select>
        </div>
      </div>
      <fieldset className="m-0 flex flex-col gap-1 border-0 p-0">
        <legend className="mb-1 p-0 text-[13px] font-medium">{t("settings.tasks.projectIcon")}</legend>
        <div className="flex flex-wrap gap-1.5">
          {([null, ...PROJECT_ICON_KEYS] as const).map((k) => {
            const Icon = k ? PROJECT_ICONS[k] : null;
            const label = t(`settings.tasks.projectIcons.${k ?? "key"}`);
            return (
              <label
                key={k ?? "key"}
                title={label}
                className={cn(
                  "grid size-9 cursor-pointer place-items-center rounded-lg border text-ink-2 has-focus-visible:outline-2 has-focus-visible:outline-teal",
                  icon === k ? "border-teal bg-teal-soft text-ink" : "border-line hover:bg-hover",
                )}
              >
                <input type="radio" name={`${prefix}-icon`} checked={icon === k} onChange={() => setIcon(k)} className="sr-only" />
                <span className="sr-only">{label}</span>
                {Icon ? (
                  <Icon aria-hidden="true" className="size-[18px]" strokeWidth={1.8} />
                ) : (
                  <span aria-hidden="true" className="font-mono text-[10.5px] font-semibold">
                    {((project?.key ?? key) || "ABC").slice(0, 3)}
                  </span>
                )}
              </label>
            );
          })}
        </div>
      </fieldset>
      <fieldset className="m-0 flex flex-col gap-1 border-0 p-0">
        <legend className="mb-1 p-0 text-[13px] font-medium">{t("settings.tasks.projectAccess")}</legend>
        {(["members", "team"] as const).map((a) => (
          <label key={a} className="flex items-start gap-2 text-[13px]">
            <input
              type="radio"
              name={`${prefix}-access`}
              value={a}
              checked={access === a}
              onChange={() => setAccess(a)}
              className="mt-0.5 size-4 accent-teal"
            />
            <span className="flex flex-col">
              {t(`settings.tasks.projectAccessOptions.${a}`)}
              <span className="text-[12px] text-ink-3">{t(`settings.tasks.projectAccessHints.${a}`)}</span>
            </span>
          </label>
        ))}
      </fieldset>
      <fieldset className="m-0 flex flex-col gap-1 border-0 p-0">
        <legend className="mb-1 p-0 text-[13px] font-medium">{t("settings.tasks.projectMembers")}</legend>
        <p className="m-0 mb-1 text-[12px] text-ink-3">
          {t(access === "team" ? "settings.tasks.projectMembersTeam" : "settings.tasks.projectMembersHint")}
        </p>
        {members
          ?.filter((m) => m.active)
          .map((m) => (
            <label key={m.user_id} className="flex items-center gap-2 text-[13px]">
              <input
                type="checkbox"
                checked={memberIds.includes(m.user_id)}
                onChange={(e) => {
                  const on = e.currentTarget.checked;
                  setMemberIds((ids) => (on ? [...ids, m.user_id] : ids.filter((id) => id !== m.user_id)));
                }}
                className="size-4 accent-teal"
              />
              {m.name}
            </label>
          ))}
      </fieldset>
      <FormMessage tone="error" message={error && error.field !== "name" ? error.message : null} />
      <div className="flex gap-2">
        <Button type="submit" size="sm" variant="primary" disabled={save.isPending}>
          {t("settings.tasks.saveProject")}
        </Button>
        <Button type="button" size="sm" onClick={onDone}>
          {t("tasks.cancel")}
        </Button>
      </div>
    </form>
  );
}
