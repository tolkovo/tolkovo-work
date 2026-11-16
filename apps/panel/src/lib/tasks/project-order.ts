import { z } from "zod";
import { create } from "zustand";

/**
 * The order of the project tiles in the rail (ADR-0022): set by each member by dragging, a
 * personal convenience of this browser, not data, so localStorage is enough. Kept per workspace;
 * projects missing from it follow in the workspace's order.
 */

export const PROJECT_ORDER_KEY = "tolkovo.tasks.projectOrder";

type Orders = Record<string, string[]>;

function readOrders(): Orders {
  try {
    const raw = window.localStorage.getItem(PROJECT_ORDER_KEY);
    if (raw === null) return {};
    return z.record(z.string(), z.array(z.string()).max(200)).catch({}).parse(JSON.parse(raw));
  } catch {
    return {};
  }
}

function writeOrders(orders: Orders): void {
  try {
    window.localStorage.setItem(PROJECT_ORDER_KEY, JSON.stringify(orders));
  } catch {
    // Storage unavailable (privacy mode, quota): the order lasts for this session only.
  }
}

type ProjectOrderState = {
  orders: Orders;
  /** Puts the ids in this order for the workspace. */
  set: (workspaceId: string, ids: string[]) => void;
};

export const useProjectOrder = create<ProjectOrderState>((set, get) => ({
  orders: readOrders(),
  set: (workspaceId, ids) => {
    const orders = { ...get().orders, [workspaceId]: ids };
    writeOrders(orders);
    set({ orders });
  },
}));

/** Items in the stored order; the ones not in it keep their own order after those. */
export function inOrder<T extends { id: string }>(items: T[], order: string[] | undefined): T[] {
  if (!order || order.length === 0) return items;
  const rank = new Map(order.map((id, i) => [id, i]));
  return items
    .map((item, i) => ({ item, key: rank.get(item.id) ?? order.length + i }))
    .sort((a, b) => a.key - b.key)
    .map((x) => x.item);
}

/** The ids with one moved from `from` to `to`. */
export function moved(ids: string[], from: number, to: number): string[] {
  if (from === to || from < 0 || to < 0 || from >= ids.length || to >= ids.length) return ids;
  const next = [...ids];
  const [id] = next.splice(from, 1);
  next.splice(to, 0, id!);
  return next;
}
