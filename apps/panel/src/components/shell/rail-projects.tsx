import { hasPermission, type TaskProject, type TaskSummary } from "@tolkovo/api-client";
import { useSession, useTaskProjects } from "@tolkovo/api-client/react";
import { Link } from "@tanstack/react-router";
import { cn } from "@tolkovo/ui";
import { Plus } from "lucide-react";
import { useEffect, useId, useRef, useState, type DragEvent, type KeyboardEvent } from "react";
import { useTranslation } from "react-i18next";

import { inOrder, moved, useProjectOrder } from "../../lib/tasks/project-order";
import { ProjectTile, projectColor } from "../tasks/project-icon";

const tileLink = cn(
  "group relative flex w-14 flex-none flex-col items-center gap-1 rounded-[10px] py-[5px]",
  "text-[10.5px] text-ink-2 no-underline hover:text-ink aria-[current=page]:text-ink",
);

/**
 * The project tiles of the rail (ADR-0022): only the projects the member is in, admins too; the
 * others open from the section's switch. The number is my open tasks in the project. The order is
 * the member's own: dragged, or moved with Alt+↑/↓. Wide screens only: on a phone the section's
 * header picks the project.
 */
export function RailProjects({ summary }: { summary: TaskSummary | undefined }) {
  const { t } = useTranslation();
  const { data: session } = useSession();
  const { data: projects } = useTaskProjects();
  const workspaceId = session?.workspace.id ?? "";
  const order = useProjectOrder((s) => s.orders[workspaceId]);
  const setOrder = useProjectOrder((s) => s.set);
  const canManage = hasPermission(session, "tasks.manage");
  const hint = useId();
  const [said, setSaid] = useState("");
  const [dragged, setDragged] = useState<string | null>(null);
  const focusAfter = useRef<string | null>(null);
  const links = useRef(new Map<string, HTMLAnchorElement>());

  const mine = inOrder(
    (projects ?? []).filter((p) => p.member && p.archived_at === null),
    order,
  );
  const ids = mine.map((p) => p.id);

  // A tile moved by the keyboard keeps the focus after the list renders again.
  useEffect(() => {
    if (!focusAfter.current) return;
    links.current.get(focusAfter.current)?.focus();
    focusAfter.current = null;
  });

  if (mine.length === 0 && !canManage) return null;

  const move = (p: TaskProject, to: number) => {
    const next = moved(ids, ids.indexOf(p.id), to);
    if (next === ids) return;
    setOrder(workspaceId, next);
    setSaid(t("nav.projects.moved", { name: p.name, position: to + 1, count: ids.length }));
  };
  const onKeyDown = (p: TaskProject, i: number) => (e: KeyboardEvent) => {
    if (!e.altKey || (e.key !== "ArrowUp" && e.key !== "ArrowDown")) return;
    e.preventDefault();
    focusAfter.current = p.id;
    move(p, e.key === "ArrowUp" ? i - 1 : i + 1);
  };
  const onDrop = (target: number) => (e: DragEvent) => {
    e.preventDefault();
    const p = mine.find((x) => x.id === dragged);
    setDragged(null);
    if (p) move(p, target);
  };

  return (
    <div className="flex min-h-0 w-full flex-col items-center max-nav:hidden">
      <div aria-hidden="true" className="mt-2 mb-1 w-10 border-t border-line" />
      <span id={`${hint}-cap`} className="mb-0.5 font-mono text-[9px] leading-none tracking-[.08em] text-ink-3 uppercase">
        {t("nav.projects.label")}
      </span>
      <ul
        aria-labelledby={`${hint}-cap`}
        className="m-0 flex min-h-0 w-full list-none flex-col items-center overflow-x-hidden overflow-y-auto p-0 [scrollbar-width:none]"
      >
        {mine.map((p, i) => {
          const open = summary?.projects.find((x) => x.project_id === p.id)?.assigned_open ?? 0;
          return (
            <li
              key={p.id}
              draggable
              onDragStart={(e) => {
                e.dataTransfer.effectAllowed = "move";
                e.dataTransfer.setData("text/plain", p.id);
                setDragged(p.id);
              }}
              onDragEnd={() => setDragged(null)}
              onDragOver={(e) => {
                if (dragged) e.preventDefault();
              }}
              onDrop={onDrop(i)}
              className={cn(dragged === p.id && "opacity-50")}
            >
              <Link
                ref={(el) => {
                  if (el) links.current.set(p.id, el);
                  else links.current.delete(p.id);
                }}
                to="/tasks"
                search={{ project: p.key }}
                activeOptions={{ includeSearch: true }}
                title={p.name}
                style={projectColor(p.color)}
                aria-describedby={mine.length > 1 ? hint : undefined}
                onKeyDown={onKeyDown(p, i)}
                className={cn(
                  tileLink,
                  "aria-[current=page]:before:absolute aria-[current=page]:before:top-2.5 aria-[current=page]:before:-left-1.5",
                  "aria-[current=page]:before:h-[26px] aria-[current=page]:before:w-[3px] aria-[current=page]:before:rounded-r-[3px]",
                  "aria-[current=page]:before:bg-(--pc)",
                )}
              >
                {({ isActive }) => (
                  <>
                    <ProjectTile icon={p.icon} color={p.color} projectKey={p.key} active={isActive} className="group-hover:rounded-[13px]" />
                    <span className="max-w-[58px] truncate">{p.name}</span>
                    {open > 0 && (
                      <span className="absolute top-px right-[5px] h-4 min-w-4 rounded-lg bg-teal px-1 text-center font-mono text-[10px] leading-4 font-semibold text-teal-ink tabular-nums">
                        <span className="sr-only">, {t("nav.myTasks", { count: open })}</span>
                        <span aria-hidden="true">{open}</span>
                      </span>
                    )}
                  </>
                )}
              </Link>
            </li>
          );
        })}
        {canManage && (
          <li>
            <Link to="/settings/tasks" hash="projects" title={t("tasks.projects.new")} className={tileLink}>
              <span className="grid size-[34px] place-items-center rounded-[10px] text-ink-3 shadow-[inset_0_0_0_1px_var(--line)] group-hover:rounded-[13px]">
                <Plus aria-hidden="true" className="size-[18px]" strokeWidth={1.8} />
              </span>
              <span className="sr-only">{t("tasks.projects.new")}</span>
            </Link>
          </li>
        )}
      </ul>
      <span id={hint} hidden>
        {t("nav.projects.reorderHint")}
      </span>
      <span role="status" className="sr-only">
        {said}
      </span>
    </div>
  );
}
