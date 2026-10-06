import type { WidgetAnalytics } from "./api";

/**
 * Goals in the site's own analytics (docs/04 §6.6): the counters the site already has, set up in
 * the panel for this widget. The widget loads no counter and sets no cookie; a counter the page
 * does not have is skipped, and a counter that throws does not break the chat. Parameters never
 * carry texts, names, contacts or ids.
 */

export type Goal = "chatStarted" | "contactCollected" | "rated";
export type GoalParams = Record<string, string | number>;

const GOAL_KEYS = { chatStarted: "chat_started", contactCollected: "contact_collected", rated: "rated" } as const;

type Track = (...args: unknown[]) => unknown;

/** What the page may have: GTM's or gtag.js's queue, gtag, Yandex Metrica. */
interface Counters {
  dataLayer?: unknown;
  gtag?: unknown;
  ym?: unknown;
}

export function report(a: WidgetAnalytics | null, goal: Goal, params: GoalParams, page: Counters = window as Counters): void {
  if (!a) return;
  const name = a.goals[GOAL_KEYS[goal]];
  const call = (fn: () => void) => {
    try {
      fn();
    } catch {
      // The site's counter is its own business.
    }
  };
  const { dataLayer, gtag, ym } = page;
  if (a.data_layer && Array.isArray(dataLayer)) call(() => void dataLayer.push({ event: name, tolkovo: { ...params } }));
  if (a.gtag && typeof gtag === "function") call(() => void (gtag as Track)("event", name, { ...params }));
  if (a.metrika_counter && typeof ym === "function") call(() => void (ym as Track)(Number(a.metrika_counter), "reachGoal", name, { ...params }));
}

/** The page without the query and the hash, which may hold anything; nothing for a local file, whose path names the computer's user. */
export function pagePath(loc: Pick<Location, "protocol" | "pathname"> = location): string {
  return loc.protocol === "file:" ? "" : loc.pathname;
}
