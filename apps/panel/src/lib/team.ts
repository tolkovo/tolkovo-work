import type { Role } from "@tolkovo/api-client";

/** Roles an admin may give in "Settings → Team" (ADR-0020); ownership is handed over separately. */
export const ASSIGNABLE_ROLES = ["admin", "operator", "programmer"] as const satisfies readonly Role[];
export type AssignableRole = (typeof ASSIGNABLE_ROLES)[number];

/**
 * Whether a role answers customers, so may take conversations; the programmer only reads the ones
 * linked to tasks (ADR-0020). A UI hint; the server decides.
 */
export function answersCustomers(role: Role): boolean {
  return role !== "programmer";
}

/** Whether an admin may change or remove this member: not the owner and not oneself. */
export function manageable(member: { user_id: string; role: Role; active: boolean }, me: string | undefined): boolean {
  return member.active && member.role !== "owner" && member.user_id !== me;
}
