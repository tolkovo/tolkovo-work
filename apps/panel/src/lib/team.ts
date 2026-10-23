import type { Role } from "@tolkovo/api-client";

/** Roles an admin may give in "Settings → Team" (ADR-0020); ownership is handed over separately. */
export const ASSIGNABLE_ROLES = ["admin", "operator"] as const satisfies readonly Role[];
export type AssignableRole = (typeof ASSIGNABLE_ROLES)[number];

/** Whether a role answers customers, so may take someone's conversations. A UI hint; the server decides. */
export function answersCustomers(role: Role): boolean {
  return role === "owner" || role === "admin" || role === "operator";
}

/** Whether an admin may change or remove this member: not the owner and not oneself. */
export function manageable(member: { user_id: string; role: Role; active: boolean }, me: string | undefined): boolean {
  return member.active && member.role !== "owner" && member.user_id !== me;
}
