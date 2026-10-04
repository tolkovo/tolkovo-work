import { useInboxCounts } from "@tolkovo/api-client/react";
import { useEffect } from "react";

export const BASE_TITLE = "Tolkovo";

/** "(3) Tolkovo"; 99+ beyond that, like the list badges. */
export function unreadTitle(count: number): string {
  if (count <= 0) return BASE_TITLE;
  return `(${count > 99 ? "99+" : count}) ${BASE_TITLE}`;
}

/**
 * The number of my conversations with unread customer messages in the tab title and a marked
 * favicon (docs/04 §5.5), so a background tab shows there is work. The counters are kept fresh
 * by realtime events.
 */
export function useUnreadTitle(): void {
  const { data } = useInboxCounts();
  const count = data?.mine_unread ?? 0;
  useEffect(() => {
    document.title = unreadTitle(count);
    const icon = document.querySelector<HTMLLinkElement>('link[rel="icon"]');
    if (icon) icon.href = count > 0 ? "/favicon-unread.svg" : "/favicon.svg";
  }, [count]);
  useEffect(
    () => () => {
      document.title = BASE_TITLE;
    },
    [],
  );
}
