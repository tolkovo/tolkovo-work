import { hasPermission, type Session } from "@tolkovo/api-client";

export type SettingsPath =
  | "/settings/general"
  | "/settings/team"
  | "/settings/profile"
  | "/settings/appearance"
  | "/settings/notifications"
  | "/settings/security";

type SettingsSection = {
  to: SettingsPath;
  label:
    | "settings.general.title"
    | "settings.team.title"
    | "settings.profile.title"
    | "settings.appearance.title"
    | "settings.notifications.title"
    | "settings.security.title";
  /** Permission from GET /me needed to see the section; none means every member sees it. */
  permission?: string;
  /** /settings may open it. Team is not a landing page: for an operator it is a read-only list. */
  landing?: boolean;
};

export type SettingsGroup = {
  label: "settings.groups.workspace" | "settings.groups.personal";
  sections: readonly SettingsSection[];
};

/**
 * Settings tree (docs/07 §7): workspace sections by permission, then the personal ones every
 * user has. More sections join with their features: the operator status joins «Уведомления»
 * with presence (Q-02), «Горячие клавиши» come with the inbox shortcuts. Hiding is a UI hint:
 * the server checks again.
 */
export const settingsGroups: readonly SettingsGroup[] = [
  {
    label: "settings.groups.workspace",
    sections: [
      { to: "/settings/general", label: "settings.general.title", permission: "workspace.manage", landing: true },
      { to: "/settings/team", label: "settings.team.title", permission: "members.view" },
    ],
  },
  {
    label: "settings.groups.personal",
    sections: [
      { to: "/settings/profile", label: "settings.profile.title", landing: true },
      { to: "/settings/appearance", label: "settings.appearance.title" },
      { to: "/settings/notifications", label: "settings.notifications.title" },
      { to: "/settings/security", label: "settings.security.title" },
    ],
  },
];

/** The groups and sections this session may open; groups left empty are dropped. */
export function visibleSettingsGroups(session: Session | null | undefined): SettingsGroup[] {
  return settingsGroups
    .map((group) => ({
      ...group,
      sections: group.sections.filter((s) => !s.permission || hasPermission(session, s.permission)),
    }))
    .filter((group) => group.sections.length > 0);
}

/**
 * Where /settings leads: the first landing section the user can open. Admins land in General,
 * everyone else (an operator) in Profile.
 */
export function firstSettingsSection(session: Session | null | undefined): SettingsPath {
  const landing = visibleSettingsGroups(session).flatMap((g) => g.sections.filter((s) => s.landing));
  return landing[0]?.to ?? "/settings/profile";
}
