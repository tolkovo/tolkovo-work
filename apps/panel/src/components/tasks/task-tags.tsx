import { isApiError, type TaskTag } from "@tolkovo/api-client";
import { useCan, useCreateTaskTag, useTaskTags } from "@tolkovo/api-client/react";
import { cn } from "@tolkovo/ui";
import { Plus } from "lucide-react";
import { useId, useState } from "react";
import { useTranslation } from "react-i18next";

import { TAG_DOT as DOT } from "../inbox/palette";
import { TagChip } from "../inbox/tags";
import { Popover } from "../menu";

/**
 * Tags of tasks: what a task is about — design, interface, backend. A list of its own, apart from
 * the tags of conversations. Any member adds a tag from the picker; admins rename and delete them
 * in the settings.
 */

/** At most this many tags on one task (TaskUpdate.tag_ids in api/openapi.yaml). */
export const MAX_TASK_TAGS = 10;

export function useTaskTagMap(): Map<string, TaskTag> {
  const { data } = useTaskTags();
  return new Map((data ?? []).map((tag) => [tag.id, tag]));
}

function pick(byId: Map<string, TaskTag>, ids: string[]): TaskTag[] {
  return ids.map((id) => byId.get(id)).filter((tag): tag is TaskTag => tag !== undefined);
}

/** The tags of a task in a row of the list: dots and names, nothing to press. */
export function TaskTagDots({ ids }: { ids: string[] }) {
  const byId = useTaskTagMap();
  const tags = pick(byId, ids);
  if (tags.length === 0) return null;
  return (
    <>
      {tags.map((tag) => (
        <span key={tag.id} className="inline-flex min-w-0 items-center gap-1 text-ink-2">
          <span aria-hidden="true" className={cn("size-2 shrink-0 rounded-full", DOT[tag.color])} />
          <span className="truncate">{tag.name}</span>
        </span>
      ))}
    </>
  );
}

type TaskTagsEditorProps = {
  selected: string[];
  /** A change is on its way: the next one would carry a stale version. */
  busy?: boolean;
  onChange: (ids: string[]) => void;
};

/** The tags of a task with the picker to add one; each change goes to onChange. */
export function TaskTagsEditor({ selected, busy = false, onChange }: TaskTagsEditorProps) {
  const { t } = useTranslation();
  const byId = useTaskTagMap();
  const tags = pick(byId, selected);
  return (
    <div className="flex flex-wrap items-center gap-1.5">
      {tags.length > 0 && (
        <ul className="m-0 flex list-none flex-wrap gap-1.5 p-0" aria-label={t("tasks.tags.label")}>
          {tags.map((tag) => (
            <li key={tag.id}>
              <TagChip tag={tag} onRemove={busy ? undefined : () => onChange(selected.filter((id) => id !== tag.id))} />
            </li>
          ))}
        </ul>
      )}
      <Popover
        label={t("tasks.tags.pick")}
        trigger={
          <>
            <Plus aria-hidden="true" className="size-3.5" />
            {t("tasks.tags.add")}
          </>
        }
        triggerClassName="inline-flex h-[22px] items-center gap-1 rounded-full border border-dashed border-line px-2 text-xs text-ink-2 hover:bg-hover hover:text-ink"
      >
        <TaskTagPicker selected={selected} busy={busy} onChange={onChange} />
      </Popover>
    </div>
  );
}

function TaskTagPicker({ selected, busy, onChange }: Required<TaskTagsEditorProps>) {
  const { t } = useTranslation();
  const { data: all = [] } = useTaskTags();
  const canCreate = useCan("tasks.edit");
  const create = useCreateTaskTag();
  const [query, setQuery] = useState("");
  const searchId = useId();
  const q = query.trim().replace(/\s+/g, " ");
  const shown = all.filter((tag) => tag.name.toLowerCase().includes(q.toLowerCase()));
  const exact = all.some((tag) => tag.name.toLowerCase() === q.toLowerCase());
  const full = selected.length >= MAX_TASK_TAGS;

  const toggle = (id: string) => onChange(selected.includes(id) ? selected.filter((s) => s !== id) : [...selected, id]);

  return (
    <div className="flex flex-col gap-1.5">
      <label htmlFor={searchId} className="sr-only">
        {t("tasks.tags.search")}
      </label>
      <input
        id={searchId}
        value={query}
        maxLength={40}
        placeholder={t("tasks.tags.search")}
        onChange={(e) => setQuery(e.target.value)}
        className="h-8 rounded-md border border-line bg-paper px-2 text-[13px]"
      />
      <ul className="m-0 flex max-h-56 list-none flex-col overflow-auto p-0">
        {shown.map((tag) => {
          const on = selected.includes(tag.id);
          return (
            <li key={tag.id}>
              <label className="flex cursor-pointer items-center gap-2 rounded px-1.5 py-1 text-[13px] hover:bg-hover">
                <input type="checkbox" checked={on} disabled={busy || (!on && full)} onChange={() => toggle(tag.id)} />
                <span aria-hidden="true" className={cn("size-2 rounded-full", DOT[tag.color])} />
                {tag.name}
              </label>
            </li>
          );
        })}
        {shown.length === 0 && !(canCreate && q) && <li className="px-1.5 py-1 text-[13px] text-ink-3">{t("tasks.tags.none")}</li>}
      </ul>
      {full && <p className="m-0 text-xs text-ink-3">{t("tasks.tags.full", { max: MAX_TASK_TAGS })}</p>}
      {canCreate && q && !exact && (
        <button
          type="button"
          disabled={create.isPending || full || busy}
          onClick={() =>
            create.mutate(
              { name: q },
              {
                onSuccess: (tag) => {
                  setQuery("");
                  onChange([...selected, tag.id]);
                },
              },
            )
          }
          className="rounded px-1.5 py-1 text-left text-[13px] text-teal hover:bg-hover disabled:text-ink-3"
        >
          {t("tasks.tags.create", { name: q })}
        </button>
      )}
      {create.isError && (
        <p role="alert" className="m-0 text-xs text-danger">
          {isApiError(create.error) && create.error.code === "tag_exists" ? t("tasks.tags.exists") : t("tasks.tags.createError")}
        </p>
      )}
    </div>
  );
}
