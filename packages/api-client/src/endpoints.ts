import { parseAnswer, unwrap, type ApiClient } from "./client";
import { isApiError } from "./errors";
import {
  conversationPageSchema,
  conversationSchema,
  deviceSessionListSchema,
  inboxCountsSchema,
  invitationListSchema,
  invitationPreviewSchema,
  invitationSentSchema,
  memberListSchema,
  messagePageSchema,
  sessionSchema,
  workspaceListSchema,
  workspaceSchema,
} from "./schemas";
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
export type UpdateMeRequest = Schemas["UpdateMeRequest"];
export type ChangePasswordRequest = Schemas["ChangePasswordRequest"];
export type DeviceSession = Schemas["DeviceSession"];
export type Problem = Schemas["Problem"];
export type Invitation = Schemas["Invitation"];
export type InvitationList = Schemas["InvitationList"];
export type InvitationSent = Schemas["InvitationSent"];
export type InvitationPreview = Schemas["InvitationPreview"];
export type CreateInvitationRequest = Schemas["CreateInvitationRequest"];
export type AcceptInvitationRequest = Schemas["AcceptInvitationRequest"];
export type ResetPasswordRequest = Schemas["ResetPasswordRequest"];
export type Conversation = Schemas["Conversation"];
export type ConversationStatus = Schemas["ConversationStatus"];
export type ConversationPreview = Schemas["ConversationPreview"];
export type ConversationPage = Schemas["ConversationPage"];
export type InboxCounts = Schemas["InboxCounts"];
export type Message = Schemas["Message"];
export type MessagePage = Schemas["MessagePage"];

/** Filter of GET /conversations: any of the statuses, and `me` or `none` (unassigned). */
export type ConversationFilter = {
  status?: ConversationStatus[];
  assignee?: "me" | "none";
};

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

/** PATCH /me: the signed-in user's name and interface language. */
export async function updateMe(client: ApiClient, body: UpdateMeRequest): Promise<Session> {
  return parseAnswer(sessionSchema, await unwrap(client.PATCH("/me", { body })));
}

/**
 * POST /me/password. The answer is a new session (new cookie and CSRF token); every other
 * session of the user is revoked by the server.
 */
export async function changePassword(client: ApiClient, body: ChangePasswordRequest): Promise<Session> {
  return parseAnswer(sessionSchema, await unwrap(client.POST("/me/password", { body })));
}

export async function fetchMySessions(client: ApiClient): Promise<DeviceSession[]> {
  return parseAnswer(deviceSessionListSchema, await unwrap(client.GET("/me/sessions"))).items;
}

export async function revokeSession(client: ApiClient, id: string): Promise<void> {
  await unwrap(client.DELETE("/me/sessions/{id}", { params: { path: { id } } }));
}

export async function revokeOtherSessions(client: ApiClient): Promise<void> {
  await unwrap(client.POST("/me/sessions/revoke-others"));
}

/** POST /me/email/verification: a new confirmation link replaces the previous one. */
export async function resendEmailVerification(client: ApiClient): Promise<void> {
  await unwrap(client.POST("/me/email/verification"));
}

/**
 * Pages behind emailed links (docs/06 A-03, A-06). The token is the URL fragment of the link;
 * the server never sees it in a URL, only in these request bodies.
 */
export async function previewInvitation(client: ApiClient, token: string): Promise<InvitationPreview> {
  return parseAnswer(
    invitationPreviewSchema,
    await unwrap(client.POST("/auth/invitation/preview", { body: { token } })),
  );
}

/** Accepts an invitation; the answer is a session in the invitation's workspace. */
export async function acceptInvitation(client: ApiClient, body: AcceptInvitationRequest): Promise<Session> {
  return parseAnswer(sessionSchema, await unwrap(client.POST("/auth/invitation/accept", { body })));
}

/** Always 202 for any address, so the answer does not reveal who has an account. */
export async function forgotPassword(client: ApiClient, email: string): Promise<void> {
  await unwrap(client.POST("/auth/password/forgot", { body: { email } }));
}

/** Sets a new password by a reset link; every session of the user ends, nobody is signed in. */
export async function resetPassword(client: ApiClient, body: ResetPasswordRequest): Promise<void> {
  await unwrap(client.POST("/auth/password/reset", { body }));
}

export async function verifyEmail(client: ApiClient, token: string): Promise<void> {
  await unwrap(client.POST("/auth/email/verify", { body: { token } }));
}

export async function fetchInvitations(client: ApiClient): Promise<InvitationList> {
  return parseAnswer(invitationListSchema, await unwrap(client.GET("/invitations")));
}

/** `invite_url` comes back only when the server cannot send email: the admin passes it on. */
export async function createInvitation(client: ApiClient, body: CreateInvitationRequest): Promise<InvitationSent> {
  return parseAnswer(invitationSentSchema, await unwrap(client.POST("/invitations", { body })), 201);
}

export async function resendInvitation(client: ApiClient, id: string): Promise<InvitationSent> {
  return parseAnswer(
    invitationSentSchema,
    await unwrap(client.POST("/invitations/{id}/resend", { params: { path: { id } } })),
  );
}

export async function revokeInvitation(client: ApiClient, id: string): Promise<void> {
  await unwrap(client.DELETE("/invitations/{id}", { params: { path: { id } } }));
}

/** One page of the inbox, most recent activity first; pass `next_cursor` for the next one. */
export async function fetchConversations(
  client: ApiClient,
  filter: ConversationFilter,
  cursor?: string,
): Promise<ConversationPage> {
  const query = { ...filter, ...(cursor ? { cursor } : {}) };
  return parseAnswer(conversationPageSchema, await unwrap(client.GET("/conversations", { params: { query } })));
}

export async function fetchConversation(client: ApiClient, id: string): Promise<Conversation> {
  return parseAnswer(
    conversationSchema,
    await unwrap(client.GET("/conversations/{id}", { params: { path: { id } } })),
  );
}

export async function fetchInboxCounts(client: ApiClient): Promise<InboxCounts> {
  return parseAnswer(inboxCountsSchema, await unwrap(client.GET("/conversations/counts")));
}

/** One page of the conversation feed, newest first. */
export async function fetchMessages(client: ApiClient, id: string, cursor?: string): Promise<MessagePage> {
  return parseAnswer(
    messagePageSchema,
    await unwrap(
      client.GET("/conversations/{id}/messages", { params: { path: { id }, query: cursor ? { cursor } : {} } }),
    ),
  );
}

/** UI hint only: the server checks every permission again (docs/00 §9.2). */
export function hasPermission(session: Session | null | undefined, permission: string): boolean {
  return session?.permissions.includes(permission) ?? false;
}
