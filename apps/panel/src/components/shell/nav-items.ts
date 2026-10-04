import { Activity, BookOpen, ChartColumn, Inbox, Settings, type LucideIcon } from "lucide-react";

export type NavItem = {
  to: "/inbox" | "/monitor" | "/analytics" | "/kb" | "/settings";
  label: "nav.inbox" | "nav.monitor" | "nav.analytics" | "nav.kb" | "nav.settings";
  icon: LucideIcon;
};

/**
 * Panel sections in rail order (docs/07 §3). Once `/me` returns permissions, items the user
 * has no right to are filtered out here, not shown disabled (docs/04 §2.2).
 */
export const navItems: readonly NavItem[] = [
  { to: "/inbox", label: "nav.inbox", icon: Inbox },
  { to: "/monitor", label: "nav.monitor", icon: Activity },
  { to: "/analytics", label: "nav.analytics", icon: ChartColumn },
  { to: "/kb", label: "nav.kb", icon: BookOpen },
  { to: "/settings", label: "nav.settings", icon: Settings },
];
