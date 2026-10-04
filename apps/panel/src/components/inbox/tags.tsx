import { isApiError, setConversationTags, type Conversation, type Tag, type TagColor } from "@tolkovo/api-client";
import { useCreateTag, useTags } from "@tolkovo/api-client/react";
import { cn } from "@tolkovo/ui";
import { Plus, X } from "lucide-react";
import { useId, useState } from "react";
import { useTranslation } from "react-i18next";

import { useAbilities, type ConversationChange } from "../../lib/inbox/actions";
import { Popover } from "../menu";

/** The dot of a tag: palette keys of the API mapped to theme tokens (packages/ui/tokens.css). */
const DOT: Record<TagColor, string> = {
  gray: "bg-ink-3",
  teal: "bg-teal",
  amber: "bg-lamp",
  red: "bg-danger",
  violet: "bg-ai",
  blue: "bg-tag-blue",
  green: "bg-tag-green",
};

/** At most this many tags on one conversation (SetTagsRequest in api/openapi.yaml). */
const MAX_TAGS = 20;

export function TagChip({ tag, onRemove }: { tag: Tag; onRemove?: (() => void) | undefined }) {
  const { t } = useTranslation();
  return (
    <span className="inline-flex h-[22px] items-center gap-1.5 rounded-full border border-line bg-surface pr-1 pl-2 text-xs text-ink-2">
      <span aria-hidden="true" className={cn("size-2 rounded-full", DOT[tag.color])} />
      {tag.name}
      {onRemove ? (
        <button
          type="button"
          aria-label={t("inbox.tags.remove", { name: tag.name })}
          onClick={onRemove}
          className="grid size-4 place-items-center rounded-full text-ink-3 hover:bg-hover hover:text-ink"
        >
          <X aria-hidden="true" className="size-3" />
        </button>
      ) : (
        <span className="w-1" />
      )}
    </span>
  );
}

/**
 * Tags of the conversation under its header (docs/04 §3.1). A change goes to the server at once
 * with the version the operator saw; who may create tags (tags.manage) can do it from the picker.
 */
export function ConversationTags({ conversation: c, change }: { conversation: Conversation; change: ConversationChange }) {
  const { t } = useTranslation();
  const can = useAbilities(c);
  const { data: all } = useTags();
  const byId = new Map((all ?? []).map((tag) => [tag.id, tag]));
  const tags = c.tag_ids.map((id) => byId.get(id)).filter((tag): tag is Tag => tag !== undefined);

  const save = (ids: string[]) => change.mutate((api) => setConversationTags(api, c.id, ids, c.version));
  if (tags.length === 0 && !can.tag) return null;

  return (
    <div className="flex flex-wrap items-center gap-1.5 border-b border-line bg-surface px-4 py-1.5 max-nav:px-3">
      <ul className="m-0 flex list-none flex-wrap gap-1.5 p-0" aria-label={t("inbox.tags.label")}>
        {tags.map((tag) => (
          <li key={tag.id}>
            <TagChip
              tag={tag}
              onRemove={can.tag && !change.isPending ? () => save(c.tag_ids.filter((id) => id !== tag.id)) : undefined}
            />
          </li>
        ))}
      </ul>
      {can.tag && (
        <Popover
          label={t("inbox.tags.pick")}
          trigger={
            <>
              <Plus aria-hidden="true" className="size-3.5" />
              {t("inbox.tags.add")}
            </>
          }
          triggerClassName="inline-flex h-[22px] items-center gap-1 rounded-full border border-dashed border-line px-2 text-xs text-ink-2 hover:bg-hover hover:text-ink"
        >
          <TagPicker
            selected={c.tag_ids}
            all={all ?? []}
            canCreate={can.createTag}
            busy={change.isPending}
            onChange={save}
          />
        </Popover>
      )}
    </div>
  );
}

type TagPickerProps = {
  selected: string[];
  all: Tag[];
  canCreate: boolean;
  /** A change is on its way: the next one would carry a stale version. */
  busy: boolean;
  onChange: (ids: string[]) => void;
};

function TagPicker({ selected, all, canCreate, busy, onChange }: TagPickerProps) {
  const { t } = useTranslation();
  const [query, setQuery] = useState("");
  const create = useCreateTag();
  const searchId = useId();
  const q = query.trim().replace(/\s+/g, " ");
  const shown = all.filter((tag) => tag.name.toLowerCase().includes(q.toLowerCase()));
  const exact = all.some((tag) => tag.name.toLowerCase() === q.toLowerCase());
  const full = selected.length >= MAX_TAGS;

  const toggle = (id: string) =>
    onChange(selected.includes(id) ? selected.filter((s) => s !== id) : [...selected, id]);

  return (
    <div className="flex flex-col gap-1.5">
      <label htmlFor={searchId} className="sr-only">
        {t("inbox.tags.search")}
      </label>
      <input
        id={searchId}
        value={query}
        maxLength={40}
        placeholder={t("inbox.tags.search")}
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
        {shown.length === 0 && !canCreate && <li className="px-1.5 py-1 text-[13px] text-ink-3">{t("inbox.tags.none")}</li>}
      </ul>
      {full && <p className="m-0 text-xs text-ink-3">{t("inbox.tags.full", { max: MAX_TAGS })}</p>}
      {canCreate && q && !exact && (
        <button
          type="button"
          disabled={create.isPending || full}
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
          {t("inbox.tags.create", { name: q })}
        </button>
      )}
      {create.isError && (
        <p role="alert" className="m-0 text-xs text-danger">
          {isApiError(create.error) && create.error.code === "tag_exists" ? t("inbox.tags.exists") : t("inbox.tags.createError")}
        </p>
      )}
    </div>
  );
}
