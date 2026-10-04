import { parseAnswer, unwrap, type ApiClient } from "./client";
import { isApiError } from "./errors";
import { memberListSchema, sessionSchema, workspaceListSchema, workspaceSchema } from "./schemas";
import type { components } from "./schema.gen";

type Schemas = components["schemas"];
export type Session = Schemas["Session"];
export type User = Schemas["User"];
export type Workspace = Schemas["Workspace"];
export type WorkspaceSummary = Schemas["WorkspaceSummary"];
export type Member = Schemas["Member"];
export type Role = Schemas["Role"];
export type Locale = Schemas["Locale"];
export type LoginRequest = Schemas["LoginRequest"];
export type RegisterRequest = Schemas["RegisterRequest"];
export type UpdateWorkspaceRequest = Schemas["UpdateWorkspaceRequest"];
export type Problem = Schemas["Problem"];

/**
 * One function per operation the panel uses. They return checked data or throw ApiError,
 * so TanStack Query and route loaders treat every failure the same way.
 */

/** GET /me. `null` means "not signed in" (401), which is an answer, not an error. */
export async function fetchSession(client: ApiClient): Promise<Session | null> {
  try {
    return parseAnswer(sessionSchema, await unwrap(client.GET("/me")));
  } catch (error) {
    if (isApiError(error) && error.status === 401) return null;
    throw error;
  }
}

export async function login(client: ApiClient, body: LoginRequest): Promise<Session> {
  return parseAnswer(sessionSchema, await unwrap(client.POST("/auth/login", { body })));
}

export async function register(client: ApiClient, body: RegisterRequest): Promise<Session> {
  return parseAnswer(sessionSchema, await unwrap(client.POST("/auth/register", { body })), 201);
}

export async function logout(client: ApiClient): Promise<void> {
  await unwrap(client.POST("/auth/logout"));
}

export async function switchWorkspace(client: ApiClient, workspaceId: string): Promise<Session> {
  return parseAnswer(
    sessionSchema,
    await unwrap(client.POST("/auth/workspace", { body: { workspace_id: workspaceId } })),
  );
}

export async function fetchMyWorkspaces(client: ApiClient): Promise<WorkspaceSummary[]> {
  return parseAnswer(workspaceListSchema, await unwrap(client.GET("/me/workspaces"))).items;
}

export async function fetchMembers(client: ApiClient): Promise<Member[]> {
  return parseAnswer(memberListSchema, await unwrap(client.GET("/members"))).items;
}

export async function updateWorkspace(client: ApiClient, body: UpdateWorkspaceRequest): Promise<Workspace> {
  return parseAnswer(workspaceSchema, await unwrap(client.PATCH("/workspace", { body })));
}

/** UI hint only: the server checks every permission again (docs/00 §9.2). */
export function hasPermission(session: Session | null | undefined, permission: string): boolean {
  return session?.permissions.includes(permission) ?? false;
}
