import {
  infiniteQueryOptions,
  queryOptions,
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueryClient,
  type QueryClient,
} from "@tanstack/react-query";
import { createContext, useContext, type ReactNode } from "react";

import type { ApiClient } from "./client";
import {
  acceptInvitation,
  changePassword,
  connectTelegramBot,
  createInvitation,
  createTag,
  disconnectChannel,
  fetchChannels,
  fetchContact,
  fetchConversation,
  fetchConversations,
  fetchInboxCounts,
  fetchNotificationSettings,
  fetchInvitations,
  fetchMembers,
  fetchMessages,
  fetchMySessions,
  fetchMyWorkspaces,
  fetchSession,
  fetchTags,
  forgotPassword,
  hasPermission,
  login,
  logout,
  previewInvitation,
  register,
  renameChannel,
  resendEmailVerification,
  resendInvitation,
  resetPassword,
  revokeInvitation,
  revokeOtherSessions,
  revokeSession,
  startTelegramUserLogin,
  submitTelegramUserCode,
  submitTelegramUserPassword,
  switchWorkspace,
  updateContact,
  updateNotificationSettings,
  updateMe,
  updateWorkspace,
  verifyEmail,
  type AcceptInvitationRequest,
  type ChangePasswordRequest,
  type ChannelList,
  type ConnectTelegramBotRequest,
  type Contact,
  type NotificationSettingsInput,
  type ContactInput,
  type ConversationFilter,
  type CreateInvitationRequest,
  type CreateTagRequest,
  type LoginRequest,
  type RegisterRequest,
  type ResetPasswordRequest,
  type Session,
  type UpdateMeRequest,
  type UpdateWorkspaceRequest,
} from "./endpoints";

/**
 * Thin TanStack Query hooks over the endpoints (docs/04 §1.2). Server state lives only in the
 * Query cache (docs/00 §7.2): the session, including the CSRF token, is the `session` query.
 */

const ApiClientContext = createContext<ApiClient | null>(null);

export function ApiClientProvider({ client, children }: { client: ApiClient; children: ReactNode }) {
  return <ApiClientContext.Provider value={client}>{children}</ApiClientContext.Provider>;
}

export function useApiClient(): ApiClient {
  const client = useContext(ApiClientContext);
  if (!client) throw new Error("useApiClient: wrap the app in <ApiClientProvider>");
  return client;
}

export const queryKeys = {
  session: ["session"] as const,
  myWorkspaces: ["me", "workspaces"] as const,
  members: ["members"] as const,
  invitations: ["invitations"] as const,
  mySessions: ["me", "sessions"] as const,
  myNotifications: ["me", "notifications"] as const,
  channels: ["channels"] as const,
  /**
   * Roots for conversation and contact queries (lists, details, message feeds nest under them):
   * realtime events invalidate by these prefixes (docs/02 §10, apps/panel/src/lib/realtime).
   */
  conversations: ["conversations"] as const,
  contacts: ["contacts"] as const,
  /** Pages of the inbox for one filter (an infinite query). */
  conversationList: (filter: ConversationFilter) => ["conversations", "list", filter] as const,
  conversation: (id: string) => ["conversations", "detail", id] as const,
  /** Pages of one conversation feed, newest first (an infinite query). */
  conversationMessages: (id: string) => ["conversations", "messages", id] as const,
  inboxCounts: ["conversations", "counts"] as const,
  contact: (id: string) => ["contacts", "detail", id] as const,
  tags: ["tags"] as const,
};

/** The current session; `null` when signed out. */
export function sessionQueryOptions(client: ApiClient) {
  return queryOptions({
    queryKey: queryKeys.session,
    queryFn: () => fetchSession(client),
    staleTime: 5 * 60_000,
  });
}

/** The CSRF token of the cached session, for createApiClient({ getCsrfToken }). */
export function csrfTokenFrom(queryClient: QueryClient): string | undefined {
  return queryClient.getQueryData<Session | null>(queryKeys.session)?.csrf_token;
}

/**
 * A new session (sign-in, sign-up, workspace switch) replaces the cached one, and every other
 * query is reset: data of the previous user or workspace must never be shown in the new one.
 */
export function startSession(queryClient: QueryClient, session: Session): void {
  queryClient.setQueryData(queryKeys.session, session);
  void queryClient.resetQueries({ predicate: (q) => q.queryKey[0] !== queryKeys.session[0] });
}

/**
 * Forgets everything the signed-out user could see. Queries only: clearing the mutation cache
 * would drop the logout mutation itself before its callbacks run.
 */
export function endSession(queryClient: QueryClient): void {
  queryClient.setQueryData(queryKeys.session, null);
  queryClient.removeQueries({ predicate: (q) => q.queryKey[0] !== queryKeys.session[0] });
}

export function useSession() {
  return useQuery(sessionQueryOptions(useApiClient()));
}

/** Whether the signed-in user's role has a permission. A UI hint; the server decides. */
export function useCan(permission: string): boolean {
  const { data } = useSession();
  return hasPermission(data, permission);
}

export function useMyWorkspaces({ enabled = true }: { enabled?: boolean } = {}) {
  const client = useApiClient();
  return useQuery({ queryKey: queryKeys.myWorkspaces, queryFn: () => fetchMyWorkspaces(client), enabled });
}

export function useMembers() {
  const client = useApiClient();
  return useQuery({ queryKey: queryKeys.members, queryFn: () => fetchMembers(client) });
}

export function useLogin() {
  const client = useApiClient();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: LoginRequest) => login(client, body),
    onSuccess: (session) => startSession(queryClient, session),
  });
}

export function useRegister() {
  const client = useApiClient();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: RegisterRequest) => register(client, body),
    onSuccess: (session) => startSession(queryClient, session),
  });
}

export function useSwitchWorkspace() {
  const client = useApiClient();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (workspaceId: string) => switchWorkspace(client, workspaceId),
    onSuccess: (session) => startSession(queryClient, session),
  });
}

/** Signs out. The local session is dropped even if the request fails: the user asked to leave. */
export function useLogout() {
  const client = useApiClient();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: () => logout(client),
    onSettled: () => endSession(queryClient),
  });
}

export function useUpdateWorkspace() {
  const client = useApiClient();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: UpdateWorkspaceRequest) => updateWorkspace(client, body),
    onSuccess: (workspace) => {
      queryClient.setQueryData<Session | null>(queryKeys.session, (s) => (s ? { ...s, workspace } : s));
      void queryClient.invalidateQueries({ queryKey: queryKeys.myWorkspaces });
    },
  });
}

/** Saves the profile; the cached session takes the server's answer (name, language). */
export function useUpdateMe() {
  const client = useApiClient();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: UpdateMeRequest) => updateMe(client, body),
    onSuccess: (session) => queryClient.setQueryData(queryKeys.session, session),
  });
}

/**
 * Changes the password. The server rotates the current session, so the cached one (with the
 * new CSRF token) is replaced; the session list changes because other devices were signed out.
 */
export function useChangePassword() {
  const client = useApiClient();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: ChangePasswordRequest) => changePassword(client, body),
    onSuccess: (session) => {
      queryClient.setQueryData(queryKeys.session, session);
      void queryClient.invalidateQueries({ queryKey: queryKeys.mySessions });
    },
  });
}

/** The signed-in user's active sessions (devices), the current one first. */
export function useMySessions() {
  const client = useApiClient();
  return useQuery({ queryKey: queryKeys.mySessions, queryFn: () => fetchMySessions(client) });
}

export function useRevokeSession() {
  const client = useApiClient();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => revokeSession(client, id),
    onSettled: () => queryClient.invalidateQueries({ queryKey: queryKeys.mySessions }),
  });
}

export function useRevokeOtherSessions() {
  const client = useApiClient();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: () => revokeOtherSessions(client),
    onSettled: () => queryClient.invalidateQueries({ queryKey: queryKeys.mySessions }),
  });
}

/** Asks for a new email confirmation link for the signed-in user. */
export function useResendEmailVerification() {
  const client = useApiClient();
  return useMutation({ mutationFn: () => resendEmailVerification(client) });
}

/**
 * What an invitation link offers. The token is part of the key only in memory; nothing is
 * retried, because an invalid link stays invalid.
 */
export function useInvitationPreview(token: string) {
  const client = useApiClient();
  return useQuery({
    queryKey: ["invitationPreview", token],
    queryFn: () => previewInvitation(client, token),
    enabled: token !== "",
    retry: false,
    staleTime: Infinity,
  });
}

/** Accepts an invitation and starts the session it returns, like sign-in. */
export function useAcceptInvitation() {
  const client = useApiClient();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: AcceptInvitationRequest) => acceptInvitation(client, body),
    onSuccess: (session) => startSession(queryClient, session),
  });
}

export function useForgotPassword() {
  const client = useApiClient();
  return useMutation({ mutationFn: (email: string) => forgotPassword(client, email) });
}

/** Resets the password. Every session ends on the server, so the local one is dropped too. */
export function useResetPassword() {
  const client = useApiClient();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: ResetPasswordRequest) => resetPassword(client, body),
    onSuccess: () => endSession(queryClient),
  });
}

/** Confirms the email; a signed-in session is refetched to show the new state. */
export function useVerifyEmail() {
  const client = useApiClient();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (token: string) => verifyEmail(client, token),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: queryKeys.session }),
  });
}

/**
 * The workspace's channels. `poll` returns the polling interval for the current list, or false to
 * stop — e.g. while the wizard waits for the first message of a new channel.
 */
export function useChannels(options: { poll?: (list: ChannelList | undefined) => number | false } = {}) {
  const client = useApiClient();
  const { poll } = options;
  return useQuery({
    queryKey: queryKeys.channels,
    queryFn: () => fetchChannels(client),
    refetchInterval: poll ? (query) => poll(query.state.data) : false,
  });
}

/** Mutations of channels refresh the list whatever the outcome: a failed step may still change it. */
function useChannelMutation<TArgs, TResult>(fn: (client: ApiClient, args: TArgs) => Promise<TResult>) {
  const client = useApiClient();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (args: TArgs) => fn(client, args),
    onSettled: () => queryClient.invalidateQueries({ queryKey: queryKeys.channels }),
  });
}

export function useConnectTelegramBot() {
  return useChannelMutation((client, body: ConnectTelegramBotRequest) => connectTelegramBot(client, body));
}

export function useStartTelegramUserLogin() {
  return useChannelMutation((client, phone: string) => startTelegramUserLogin(client, phone));
}

export function useSubmitTelegramUserCode() {
  return useChannelMutation((client, { id, code }: { id: string; code: string }) => submitTelegramUserCode(client, id, code));
}

export function useSubmitTelegramUserPassword() {
  return useChannelMutation((client, { id, password }: { id: string; password: string }) =>
    submitTelegramUserPassword(client, id, password),
  );
}

export function useRenameChannel() {
  return useChannelMutation((client, { id, name }: { id: string; name: string }) => renameChannel(client, id, name));
}

export function useDisconnectChannel() {
  return useChannelMutation((client, id: string) => disconnectChannel(client, id));
}

export function useInvitations({ enabled = true }: { enabled?: boolean } = {}) {
  const client = useApiClient();
  return useQuery({ queryKey: queryKeys.invitations, queryFn: () => fetchInvitations(client), enabled });
}

export function useCreateInvitation() {
  const client = useApiClient();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: CreateInvitationRequest) => createInvitation(client, body),
    onSettled: () => queryClient.invalidateQueries({ queryKey: queryKeys.invitations }),
  });
}

export function useResendInvitation() {
  const client = useApiClient();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => resendInvitation(client, id),
    onSettled: () => queryClient.invalidateQueries({ queryKey: queryKeys.invitations }),
  });
}

export function useRevokeInvitation() {
  const client = useApiClient();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => revokeInvitation(client, id),
    onSettled: () => queryClient.invalidateQueries({ queryKey: queryKeys.invitations }),
  });
}

/** The inbox for one filter, page by page; realtime events patch it in place (apps/panel/src/lib/realtime). */
export function conversationListQueryOptions(client: ApiClient, filter: ConversationFilter) {
  return infiniteQueryOptions({
    queryKey: queryKeys.conversationList(filter),
    queryFn: ({ pageParam }) => fetchConversations(client, filter, pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next_cursor ?? undefined,
  });
}

export function useConversationList(filter: ConversationFilter) {
  return useInfiniteQuery(conversationListQueryOptions(useApiClient(), filter));
}

export function conversationQueryOptions(client: ApiClient, id: string) {
  return queryOptions({ queryKey: queryKeys.conversation(id), queryFn: () => fetchConversation(client, id) });
}

export function useConversation(id: string) {
  return useQuery(conversationQueryOptions(useApiClient(), id));
}

export function useInboxCounts() {
  const client = useApiClient();
  return useQuery({ queryKey: queryKeys.inboxCounts, queryFn: () => fetchInboxCounts(client) });
}

/** The conversation feed, newest page first; each page is newest first too. */
export function useMessages(id: string) {
  const client = useApiClient();
  return useInfiniteQuery({
    queryKey: queryKeys.conversationMessages(id),
    queryFn: ({ pageParam }) => fetchMessages(client, id, pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next_cursor ?? undefined,
  });
}

/** Every tag of the workspace by name; there are at most 500, so no pages. */
export function useTags() {
  const client = useApiClient();
  return useQuery({ queryKey: queryKeys.tags, queryFn: () => fetchTags(client), staleTime: 5 * 60_000 });
}

export function useCreateTag() {
  const client = useApiClient();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: CreateTagRequest) => createTag(client, body),
    onSettled: () => queryClient.invalidateQueries({ queryKey: queryKeys.tags }),
  });
}

export function useContact(id: string) {
  const client = useApiClient();
  return useQuery({ queryKey: queryKeys.contact(id), queryFn: () => fetchContact(client, id) });
}

/**
 * Saves contact fields; the answer replaces the cached contact. Conversations that show the name
 * are the caller's to refresh (the panel knows which of its cached lists show this contact).
 */
export function useUpdateContact(id: string) {
  const client = useApiClient();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: ContactInput) => updateContact(client, id, body),
    onSuccess: (contact: Contact) => {
      queryClient.setQueryData(queryKeys.contact(id), contact);
    },
  });
}

export function useNotificationSettings() {
  const client = useApiClient();
  return useQuery({ queryKey: queryKeys.myNotifications, queryFn: () => fetchNotificationSettings(client) });
}

/** Saves the given fields; the answer replaces the cached settings. */
export function useUpdateNotificationSettings() {
  const client = useApiClient();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: NotificationSettingsInput) => updateNotificationSettings(client, body),
    onSuccess: (settings) => queryClient.setQueryData(queryKeys.myNotifications, settings),
  });
}
