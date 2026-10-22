import { useTaskQueues, useTaskTags, useTopics } from "@tolkovo/api-client/react";
import { Link, useNavigate } from "@tanstack/react-router";
import { Select, cn } from "@tolkovo/ui";
import { LayoutList, Search, SquareKanban } from "lucide-react";
import { useEffect, useId, useState } from "react";
import { useTranslation } from "react-i18next";

import { storeTasksLayout } from "../../lib/preferences";
import type { TaskLayout, TasksSearch } from "../../lib/tasks/tasks";

/**
 * The filters the list and the board share: search, queue, topic and tag, kept in the URL.
 * The list stacks them, the board puts them in a row.
 */
export function TaskFilters({ search, className }: { search: TasksSearch; className?: string }) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { data: queues } = useTaskQueues();
  const { data: topics } = useTopics();
  const { data: tags } = useTaskTags();
  const queueId = useId();
  const topicId = useId();
  const tagId = useId();
  const searchId = useId();
  const [query, setQuery] = useState(search.q ?? "");
  // The search goes to the URL after a pause in typing, so every letter is not a request.
  useEffect(() => {
    const q = query.trim();
    if (q === (search.q ?? "")) return;
    const timer = setTimeout(() => void navigate({ to: ".", search: (s: TasksSearch) => ({ ...s, q: q || undefined }) }), 300);
    return () => clearTimeout(timer);
  }, [query, search.q, navigate]);
  const set = (patch: Partial<TasksSearch>) => void navigate({ to: ".", search: (s: TasksSearch) => ({ ...s, ...patch }) });

  return (
    <div className={className}>
      <div className="relative min-w-0">
        <label htmlFor={searchId} className="sr-only">
          {t("tasks.search")}
        </label>
        <Search aria-hidden="true" className="pointer-events-none absolute top-2.5 left-2.5 size-4 text-ink-3" />
        <input
          id={searchId}
          type="search"
          value={query}
          maxLength={100}
          placeholder={t("tasks.searchPlaceholder")}
          onChange={(e) => setQuery(e.currentTarget.value)}
          className="h-9 w-full min-w-0 rounded-lg border border-input bg-surface pr-2.5 pl-8 text-sm text-ink focus-visible:border-ring focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-ring"
        />
      </div>
      {(queues?.length ?? 0) > 0 && (
        <div className="min-w-0">
          <label htmlFor={queueId} className="sr-only">
            {t("tasks.queueFilter")}
          </label>
          <Select id={queueId} value={search.queue ?? ""} onChange={(e) => set({ queue: e.currentTarget.value || undefined })}>
            <option value="">{t("tasks.allQueues")}</option>
            {queues?.map((q) => (
              <option key={q.id} value={q.id}>
                {q.name}
              </option>
            ))}
            <option value="none">{t("tasks.noQueue")}</option>
          </Select>
        </div>
      )}
      {(topics?.length ?? 0) > 0 && (
        <div className="min-w-0">
          <label htmlFor={topicId} className="sr-only">
            {t("tasks.topicFilter")}
          </label>
          <Select id={topicId} value={search.topic ?? ""} onChange={(e) => set({ topic: e.currentTarget.value || undefined })}>
            <option value="">{t("tasks.allTopics")}</option>
            {topics?.map((topic) => (
              <option key={topic.id} value={topic.id}>
                {topic.name}
              </option>
            ))}
            <option value="none">{t("inbox.topic.without")}</option>
          </Select>
        </div>
      )}
      {(tags?.length ?? 0) > 0 && (
        <div className="min-w-0">
          <label htmlFor={tagId} className="sr-only">
            {t("tasks.tags.filter")}
          </label>
          <Select id={tagId} value={search.tag ?? ""} onChange={(e) => set({ tag: e.currentTarget.value || undefined })}>
            <option value="">{t("tasks.tags.all")}</option>
            {tags?.map((tag) => (
              <option key={tag.id} value={tag.id}>
                {tag.name}
              </option>
            ))}
          </Select>
        </div>
      )}
    </div>
  );
}

/** "List | Board": the choice goes to the URL and is remembered for the next visit. */
export function LayoutSwitch({ search, layout }: { search: TasksSearch; layout: TaskLayout }) {
  const { t } = useTranslation();
  const options = [
    { id: "list", Icon: LayoutList },
    { id: "board", Icon: SquareKanban },
  ] as const;
  return (
    <nav aria-label={t("tasks.layout.label")} className="inline-flex shrink-0 rounded-lg border border-line bg-sunk p-0.5">
      {options.map(({ id, Icon }) => (
        <Link
          key={id}
          to="/tasks"
          search={{ ...search, layout: id }}
          onClick={() => storeTasksLayout(id)}
          aria-current={layout === id ? "page" : undefined}
          className={cn(
            "flex h-6.5 items-center gap-1.5 rounded-md px-2.5 text-[13px] text-ink-2 no-underline hover:text-ink",
            "aria-[current=page]:bg-surface aria-[current=page]:font-medium aria-[current=page]:text-ink aria-[current=page]:shadow-xs",
          )}
        >
          <Icon aria-hidden="true" className="size-3.5" />
          {t(`tasks.layout.${id}`)}
        </Link>
      ))}
    </nav>
  );
}
