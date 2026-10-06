import { Activity, BookOpen, ChartColumn, Inbox, ListTodo, Settings, type LucideIcon } from "lucide-react";

export type NavItem = {
  to: "/inbox" | "/tasks" | "/monitor" | "/analytics" | "/kb" | "/settings";
  label: "nav.inbox" | "nav.tasks" | "nav.monitor" | "nav.analytics" | "nav.kb" | "nav.settings";
  icon: LucideIcon;
  /** Permission from GET /me needed to see the item; none means every member sees it. */
  permission?: string;
};

/**
 * Panel sections in rail order (docs/07 §3). Items the user has no right to are left out,
 * not shown disabled (docs/04 §2.2). Monitor, analytics and the knowledge base get their
 * permissions with their features; until then every member sees them. Settings is for
 * everyone: personal settings belong to every role, workspace sections are filtered inside.
 */
export const navItems: readonly NavItem[] = [
  { to: "/inbox", label: "nav.inbox", icon: Inbox },
  // The tasks section (ADR-0015); hidden while an admin has it turned off.
  { to: "/tasks", label: "nav.tasks", icon: ListTodo, permission: "tasks.view" },
  { to: "/monitor", label: "nav.monitor", icon: Activity },
  { to: "/analytics", label: "nav.analytics", icon: ChartColumn },
  { to: "/kb", label: "nav.kb", icon: BookOpen },
  { to: "/settings", label: "nav.settings", icon: Settings },
];
