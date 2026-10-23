import type { Task, TaskBoard } from "@tolkovo/api-client";
import { describe, expect, it } from "vitest";

import { boardFilter, compareInColumn, comparePriorities, daysInColumn, lanesOf, neighbourStatus, overLimit, placeTask, standsTooLong } from "./board";
import { taskUpdate } from "./tasks";

const task = (number: number, patch: Partial<Task> = {}): Task => ({
  id: `t${number}`,
  number,
  queue_id: null,
  topic_id: null,
  title: `Задача ${number}`,
  body: "",
  status: "open",
  priority: "normal",
  assignee_id: null,
  created_by: null,
  created_at: "2026-10-01T10:00:00Z",
  updated_at: "2026-10-01T10:00:00Z",
  closed_at: null,
  status_changed_at: "2026-10-10T10:00:00Z",
  blocked_reason: null,
  version: 1,
  conversation_ids: [],
  conversation_count: 0,
  files: [],
  tag_ids: [],
  ...patch,
});

const numbers = (items: Task[]) => items.map((x) => x.number);

describe("the order of a column", () => {
  it("puts the most urgent first, then the one standing longest", () => {
    const items = [
      task(1, { priority: "low", status_changed_at: "2026-10-01T00:00:00Z" }),
      task(2, { status_changed_at: "2026-10-09T00:00:00Z" }),
      task(3, { status_changed_at: "2026-10-05T00:00:00Z" }),
      task(4, { priority: "urgent", status_changed_at: "2026-10-20T00:00:00Z" }),
      task(5, { priority: "high" }),
    ];
    expect(numbers(items.sort(compareInColumn))).toEqual([4, 5, 3, 2, 1]);
  });

  it("puts the latest closed first in done", () => {
    const items = [
      task(1, { status: "done", status_changed_at: "2026-10-01T00:00:00Z", priority: "urgent" }),
      task(2, { status: "done", status_changed_at: "2026-10-09T00:00:00Z" }),
    ];
    expect(numbers(items.sort(compareInColumn))).toEqual([2, 1]);
  });
});

describe("moving a card before the server answers", () => {
  const board = (): TaskBoard => ({
    columns: [
      { status: "open", count: 2, items: [task(1), task(2, { status_changed_at: "2026-10-01T00:00:00Z" })], next_cursor: null, wip_limit: null, age_days: null },
      { status: "in_progress", count: 1, items: [task(3, { status: "in_progress", assignee_id: "u-petr", status_changed_at: "2026-10-01T00:00:00Z" })], next_cursor: null, wip_limit: null, age_days: null },
    ],
  });
  const now = new Date("2026-10-22T12:00:00Z");

  it("takes the card out of its column and puts it in order in the new one, given to me", () => {
    const moving = task(1);
    const out = placeTask(board(), moving, taskUpdate(moving, { status: "in_progress" }), "u-anna", now);
    expect(out.columns.map((c) => [c.status, c.count, numbers(c.items)])).toEqual([
      ["open", 1, [2]],
      ["in_progress", 2, [3, 1]],
    ]);
    const moved = out.columns[1]?.items[1];
    expect(moved).toMatchObject({ status: "in_progress", assignee_id: "u-anna", status_changed_at: now.toISOString() });
  });

  it("keeps the assignee and the time in the column when only the assignee changes", () => {
    const moving = task(2, { status_changed_at: "2026-10-01T00:00:00Z" });
    const out = placeTask(board(), moving, taskUpdate(moving, { assignee_id: "u-anna" }), "u-anna", now);
    expect(out.columns[0]?.count).toBe(2);
    expect(out.columns[0]?.items.find((x) => x.number === 2)).toMatchObject({ assignee_id: "u-anna", status_changed_at: "2026-10-01T00:00:00Z" });
  });
});

describe("the board's filter", () => {
  it("turns the list's views into the assignee and drops the state", () => {
    expect(boardFilter({ view: "mine", q: " принтер " })).toEqual({ assignee: "me", q: "принтер" });
    expect(boardFilter({ view: "unassigned", queue: "none" })).toEqual({ assignee: "none", queue: "none" });
    expect(boardFilter({ view: "closed", tag: "g1" })).toEqual({ tag: "g1" });
    expect(boardFilter({})).toEqual({});
  });
});

describe("neighbours and ages", () => {
  it("knows the next and previous column, and nothing past done", () => {
    expect(neighbourStatus("in_progress", 1)).toBe("review");
    expect(neighbourStatus("open", -1)).toBeUndefined();
    expect(neighbourStatus("done", 1)).toBeUndefined();
    expect(neighbourStatus("canceled", 1)).toBeUndefined();
  });

  it("counts whole days in the column", () => {
    const now = new Date("2026-10-14T09:00:00Z");
    expect(daysInColumn(task(1), now)).toBe(3);
    expect(daysInColumn(task(1, { status_changed_at: "2026-10-14T08:00:00Z" }), now)).toBe(0);
  });
});

describe("limits, ages and the blocked mark", () => {
  it("counts over the soft limit only past it, and nothing without one", () => {
    expect(overLimit(3, 3)).toBe(false);
    expect(overLimit(4, 3)).toBe(true);
    expect(overLimit(40, null)).toBe(false);
  });

  it("finds a card standing too long only in an open column with a threshold", () => {
    const now = new Date("2026-10-14T10:00:00Z");
    expect(standsTooLong(task(1), 3, now)).toBe(true);
    expect(standsTooLong(task(1), 5, now)).toBe(false);
    expect(standsTooLong(task(1), null, now)).toBe(false);
    expect(standsTooLong(task(1, { status: "done" }), 1, now)).toBe(false);
  });

  it("keeps the mark of a move that does not name it, and closing takes it off", () => {
    const board: TaskBoard = { columns: [{ status: "open", count: 1, items: [], next_cursor: null, wip_limit: null, age_days: null }] };
    const now = new Date("2026-10-22T12:00:00Z");
    const blocked = task(1, { blocked_reason: "Ждём доступ" });
    const at = (b: TaskBoard, status: string) => b.columns.find((c) => c.status === status)?.items[0];
    const withDone: TaskBoard = { columns: [...board.columns, { status: "done", count: 0, items: [], next_cursor: null, wip_limit: null, age_days: null }] };
    expect(at(placeTask(board, blocked, taskUpdate(blocked, { priority: "high" }), undefined, now), "open")?.blocked_reason).toBe("Ждём доступ");
    expect(at(placeTask(board, blocked, taskUpdate(blocked, { blocked_reason: "" }), undefined, now), "open")?.blocked_reason).toBeNull();
    expect(at(placeTask(withDone, blocked, taskUpdate(blocked, { status: "done" }), undefined, now), "done")?.blocked_reason).toBeNull();
  });
});

describe("swimlanes", () => {
  const board: TaskBoard = {
    columns: [
      { status: "open", count: 2, items: [task(1, { assignee_id: "u-b" }), task(2)], next_cursor: null, wip_limit: null, age_days: null },
      { status: "in_progress", count: 2, items: [task(3, { assignee_id: "u-a", priority: "urgent" }), task(4, { assignee_id: "u-b", priority: "low" })], next_cursor: null, wip_limit: null, age_days: null },
    ],
  };
  const names: Record<string, string> = { "u-a": "Анна", "u-b": "Борис" };

  it("groups the cards by assignee, by name, the unassigned last", () => {
    const lanes = lanesOf(board, "assignee", (a, b) => (names[a] ?? "").localeCompare(names[b] ?? ""));
    expect(lanes.map((l) => [l.key, numbers(l.tasks)])).toEqual([
      ["u-a", [3]],
      ["u-b", [1, 4]],
      ["", [2]],
    ]);
  });

  it("groups by priority, the most urgent first", () => {
    expect(lanesOf(board, "priority", comparePriorities).map((l) => l.key)).toEqual(["urgent", "normal", "low"]);
  });
});
