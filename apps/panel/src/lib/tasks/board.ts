import type { Task, TaskBoard, TaskBoardFilter, TaskStatus, TaskUpdate } from "@tolkovo/api-client";

import { NEXT_STATUS, type TaskLanes, type TasksSearch } from "./tasks";

/** The board of tasks (ADR-0019): a column by status, in the order of the path. */

export const BOARD_STATUSES: readonly TaskStatus[] = ["open", "in_progress", "review", "verified", "done"];

/** Priorities on the board, most urgent first; the server orders by the same rank. */
const PRIORITY_RANK: Record<Task["priority"], number> = { urgent: 0, high: 1, normal: 2, low: 3 };

/**
 * The board's filter of a search: the list's views become the assignee filter, the columns
 * part open from closed themselves. Without a view the board is the whole team's.
 */
export function boardFilter({ view, queue, topic, tag, blocked, q }: TasksSearch): TaskBoardFilter {
  const filter: TaskBoardFilter = {};
  if (view === "mine") filter.assignee = "me";
  if (view === "unassigned") filter.assignee = "none";
  if (queue) filter.queue = queue;
  if (topic) filter.topic = topic;
  if (tag) filter.tag = tag;
  if (blocked) filter.blocked = true;
  if (q) filter.q = q.trim();
  return filter;
}

function isClosedStatus(status: TaskStatus): boolean {
  return status === "done" || status === "canceled";
}

/**
 * The order of a column, as the server sorts it: an open column puts the most urgent first, then
 * the one standing longest; done and canceled the latest first.
 */
export function compareInColumn(a: Task, b: Task): number {
  const at = Date.parse(a.status_changed_at);
  const bt = Date.parse(b.status_changed_at);
  if (isClosedStatus(a.status)) return bt - at || b.number - a.number;
  return PRIORITY_RANK[a.priority] - PRIORITY_RANK[b.priority] || at - bt || a.number - b.number;
}

/**
 * The board with the task moved by an update, before the server answers: the card leaves its
 * column and takes its place in the new one. In progress without an assignee goes to `me`,
 * as the server does.
 */
export function placeTask(board: TaskBoard, task: Task, body: TaskUpdate, me: string | undefined, now: Date): TaskBoard {
  const status = body.status;
  const moved: Task = {
    ...task,
    status,
    priority: body.priority,
    assignee_id: status === "in_progress" && task.status !== "in_progress" && !body.assignee_id ? (me ?? null) : (body.assignee_id ?? null),
    status_changed_at: status === task.status ? task.status_changed_at : now.toISOString(),
    // Absent keeps the mark, "" takes it off; closing takes it off too.
    blocked_reason: isClosedStatus(status) ? null : body.blocked_reason === undefined ? task.blocked_reason : body.blocked_reason.trim() || null,
  };
  return {
    columns: board.columns.map((col) => {
      const had = col.items.some((it) => it.id === task.id);
      let items = col.items.filter((it) => it.id !== task.id);
      let count = had ? col.count - 1 : col.count;
      if (col.status === status) {
        items = [...items, moved].sort(compareInColumn);
        count += 1;
      }
      return { ...col, items, count };
    }),
  };
}

/** The step the board suggests after a status: a new task is taken into work first. */
export function nextStep(status: TaskStatus): TaskStatus | undefined {
  return status === "open" ? "in_progress" : NEXT_STATUS[status];
}

/** The column after or before a status on the board, for Shift+→ and Shift+←. */
export function neighbourStatus(status: TaskStatus, step: 1 | -1): TaskStatus | undefined {
  const i = BOARD_STATUSES.indexOf(status);
  return i < 0 ? undefined : BOARD_STATUSES[i + step];
}

/** Whole days a task stands in its column, for the age on the card; under a day is 0. */
export function daysInColumn(task: Task, now: Date): number {
  return Math.max(0, Math.floor((now.getTime() - Date.parse(task.status_changed_at)) / 86_400_000));
}

/** Hours under a day, for a card moved today. */
export function hoursInColumn(task: Task, now: Date): number {
  return Math.max(0, Math.floor((now.getTime() - Date.parse(task.status_changed_at)) / 3_600_000));
}

/** Over the soft limit of its column (ADR-0019, K2): shown, never refused. */
export function overLimit(count: number, limit: number | null): boolean {
  return limit !== null && count > limit;
}

/** Standing in its column at least the column's age threshold. */
export function standsTooLong(task: Task, ageDays: number | null, now: Date): boolean {
  return ageDays !== null && !isClosedStatus(task.status) && daysInColumn(task, now) >= ageDays;
}

export type Lane = { key: string; tasks: Task[] };

/**
 * Swimlanes: the board's cards grouped by assignee, queue or priority, every group with the
 * cards of all its columns. A lane's key is the id or the priority, "" for none, which goes
 * last; `order` sorts the others (names, or urgent first).
 */
export function lanesOf(board: TaskBoard, by: TaskLanes, order: (a: string, b: string) => number): Lane[] {
  const lanes = new Map<string, Task[]>();
  for (const col of board.columns) {
    for (const task of col.items) {
      const key = (by === "assignee" ? task.assignee_id : by === "queue" ? task.queue_id : task.priority) ?? "";
      lanes.set(key, [...(lanes.get(key) ?? []), task]);
    }
  }
  return [...lanes.entries()]
    .map(([key, tasks]) => ({ key, tasks }))
    .sort((a, b) => (a.key === "") !== (b.key === "") ? Number(a.key === "") - Number(b.key === "") : order(a.key, b.key));
}

/** Priorities as lanes: the most urgent first. */
export function comparePriorities(a: string, b: string): number {
  return (PRIORITY_RANK[a as Task["priority"]] ?? 9) - (PRIORITY_RANK[b as Task["priority"]] ?? 9);
}
