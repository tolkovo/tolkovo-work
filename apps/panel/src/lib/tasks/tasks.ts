import type { Task, TaskFilter, TaskPriority, TaskStatus, TaskUpdate } from "@tolkovo/api-client";

/** The tasks section (ADR-0015, docs/04 §3.6): views of the list, kept in the URL. */

export const TASK_VIEWS = ["mine", "open", "unassigned", "closed", "all"] as const;
export type TaskView = (typeof TASK_VIEWS)[number];

export const TASK_STATUSES: readonly TaskStatus[] = ["open", "in_progress", "review", "verified", "done", "canceled"];
export const TASK_PRIORITIES: readonly TaskPriority[] = ["urgent", "high", "normal", "low"];

/** The section as a list with the open task beside it, as the board, or its flow metrics (ADR-0019). */
export const TASK_LAYOUTS = ["list", "board", "flow"] as const;
export type TaskLayout = (typeof TASK_LAYOUTS)[number];

/** Swimlanes of the board (ADR-0019, K2): cards grouped by who, which queue or how urgent. */
export const TASK_LANES = ["assignee", "queue", "priority"] as const;
export type TaskLanes = (typeof TASK_LANES)[number];

export type TasksSearch = {
  layout?: TaskLayout;
  lanes?: TaskLanes;
  /** Only the blocked tasks. */
  blocked?: true;
  view?: TaskView;
  /** A queue id or `none`. */
  queue?: string;
  /** A topic id or `none`. */
  topic?: string;
  /** A tag id of tasks. */
  tag?: string;
  q?: string;
  /** Days back the flow metrics reach. */
  days?: FlowDays;
  /** On the board, the open task takes the whole page instead of the side panel. */
  full?: true;
};

/** The search for links that lead away from the open task: `full` belongs to that task only. */
export function withoutFull({ full: _full, ...rest }: TasksSearch): TasksSearch {
  return rest;
}

/** Periods of the flow metrics (ADR-0019, K3). */
export const FLOW_DAYS = [30, 90] as const;
export type FlowDays = (typeof FLOW_DAYS)[number];

/** Keeps only known values from the URL: a hand-edited link never breaks the page. */
export function validateTasksSearch(search: Record<string, unknown>): TasksSearch {
  const out: TasksSearch = {};
  if (typeof search.layout === "string" && (TASK_LAYOUTS as readonly string[]).includes(search.layout)) out.layout = search.layout as TaskLayout;
  if (typeof search.lanes === "string" && (TASK_LANES as readonly string[]).includes(search.lanes)) out.lanes = search.lanes as TaskLanes;
  if (search.blocked === true || search.blocked === "true") out.blocked = true;
  if (search.full === true || search.full === "true") out.full = true;
  if (typeof search.view === "string" && (TASK_VIEWS as readonly string[]).includes(search.view)) out.view = search.view as TaskView;
  if (typeof search.queue === "string" && /^(none|[0-9a-f-]{36})$/.test(search.queue)) out.queue = search.queue;
  if (typeof search.topic === "string" && /^(none|[0-9a-f-]{36})$/.test(search.topic)) out.topic = search.topic;
  if (typeof search.tag === "string" && /^[0-9a-f-]{36}$/.test(search.tag)) out.tag = search.tag;
  if (typeof search.q === "string" && search.q.trim() !== "") out.q = search.q.slice(0, 100);
  const days = Number(search.days);
  if ((FLOW_DAYS as readonly number[]).includes(days)) out.days = days as FlowDays;
  return out;
}

/** The API filter of a view. */
export function taskFilter({ view = "mine", queue, topic, tag, blocked, q }: TasksSearch): TaskFilter {
  const filter: TaskFilter = {};
  switch (view) {
    case "mine":
      filter.state = "open";
      filter.assignee = "me";
      break;
    case "open":
      filter.state = "open";
      break;
    case "unassigned":
      filter.state = "open";
      filter.assignee = "none";
      break;
    case "closed":
      filter.state = "closed";
      break;
    case "all":
      break;
  }
  if (queue) filter.queue = queue;
  if (topic) filter.topic = topic;
  if (tag) filter.tag = tag;
  if (blocked) filter.blocked = true;
  if (q) filter.q = q.trim();
  return filter;
}

export function isClosed(status: TaskStatus): boolean {
  return status === "done" || status === "canceled";
}

/**
 * The step the panel suggests after a status: to review on the dev server, verified, then done
 * in production. Only a hint: any status can be picked.
 */
export const NEXT_STATUS: Partial<Record<TaskStatus, TaskStatus>> = { in_progress: "review", review: "verified", verified: "done" };

/** The body of PUT /tasks/{id}: the task as read, with the change applied. */
export function taskUpdate(task: Task, change: Partial<Omit<TaskUpdate, "version">> = {}): TaskUpdate {
  return {
    version: task.version,
    title: task.title,
    body: task.body,
    status: task.status,
    priority: task.priority,
    queue_id: task.queue_id,
    topic_id: task.topic_id,
    assignee_id: task.assignee_id,
    ...change,
  };
}
