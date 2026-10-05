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
  connectAppChannel,
  connectWidgetChannel,
  connectTelegramBot,
  createIngestKey,
  createInvitation,
  createTag,
  disconnectChannel,
  fetchAttachmentLink,
  fetchRegistrationOpen,
  fetchChannels,
  fetchContact,
  fetchIngestKeys,
  fetchConversation,
  fetchConversations,
  fetchInboxCounts,
  fetchNotificationSettings,
  fetchInvitations,
  fetchMembers,
  fetchMessages,
  fetchMySessions,
  fetchGitHubIntegration,
  fetchGitHubRepositories,
  startGitHubIntegration,
  completeGitHubApp,
  completeGitHubInstallation,
  updateGitHubIntegration,
  disconnectGitHubIntegration,
  fetchConversationIssues,
  fetchIssueDraft,
  fetchOpenGitHubIssues,
  createConversationIssue,
  linkConversationIssue,
  retryConversationIssue,
  dismissConversationIssue,
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
  rotateIdentitySecret,
  setAppLimits,
  setWidgetOrigins,
  revokeIngestKey,
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
  type AppLimits,
  type UpdateGitHubIntegrationRequest,
  type ChangePasswordRequest,
  type ChannelList,
  type ConnectTelegramBotRequest,
  type Contact,
  type NotificationSettingsInput,
  type ContactInput,
  type ConversationFilter,
  type CreateInvitationRequest,
  type CreateIssueRequest,
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
  registration: ["auth", "registration"] as const,
  myWorkspaces: ["me", "workspaces"] as const,
  members: ["members"] as const,
  invitations: ["invitations"] as const,
  mySessions: ["me", "sessions"] as const,
  myNotifications: ["me", "notifications"] as const,
  channels: ["channels"] as const,
  ingestKeys: (channelId: string) => ["channels", "ingest-keys", channelId] as const,
  githubIntegration: ["integrations", "github"] as const,
  githubRepositories: ["integrations", "github", "repositories"] as const,
  openGitHubIssues: ["integrations", "github", "issues"] as const,
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
  /** GitHub issues of one conversation (GH-02). */
  conversationIssues: (id: string) => ["conversations", "issues", id] as const,
  issueDraft: (id: string) => ["conversations", "issue-draft", id] as const,
  contact: (id: string) => ["contacts", "detail", id] as const,
  tags: ["tags"] as const,
  attachmentLink: (id: string) => ["attachments", "link", id] as const,
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

/** Whether the server takes new accounts: the panel offers "Create workspace" only then. */
export function useRegistrationOpen() {
  const client = useApiClient();
  return useQuery({ queryKey: queryKeys.registration, queryFn: () => fetchRegistrationOpen(client), staleTime: 60_000 });
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
export function useChannels(
  options: { poll?: (list: ChannelList | undefined) => number | false; enabled?: boolean } = {},
) {
  const client = useApiClient();
  const { poll, enabled = true } = options;
  return useQuery({
    queryKey: queryKeys.channels,
    queryFn: () => fetchChannels(client),
    refetchInterval: poll ? (query) => poll(query.state.data) : false,
    enabled,
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

/** App channels (docs/02 §11.6). Every mutation refreshes the channels and their keys. */
export function useConnectAppChannel() {
  return useChannelMutation((client, name: string) => connectAppChannel(client, name));
}

export function useIngestKeys(channelId: string, { enabled = true }: { enabled?: boolean } = {}) {
  const client = useApiClient();
  return useQuery({ queryKey: queryKeys.ingestKeys(channelId), queryFn: () => fetchIngestKeys(client, channelId), enabled });
}

export function useCreateIngestKey() {
  return useChannelMutation((client, { id, name }: { id: string; name: string }) => createIngestKey(client, id, name));
}

export function useRevokeIngestKey() {
  return useChannelMutation((client, { id, keyId }: { id: string; keyId: string }) => revokeIngestKey(client, id, keyId));
}

export function useRotateIdentitySecret() {
  return useChannelMutation((client, id: string) => rotateIdentitySecret(client, id));
}

export function useSetAppLimits() {
  return useChannelMutation((client, { id, limits }: { id: string; limits: AppLimits }) => setAppLimits(client, id, limits));
}

/** Chat widgets (docs/04 §6, W-01). */
export function useConnectWidgetChannel() {
  return useChannelMutation((client, body: { name: string; allowed_origins: string[] }) => connectWidgetChannel(client, body));
}

export function useSetWidgetOrigins() {
  return useChannelMutation((client, { id, origins }: { id: string; origins: string[] }) => setWidgetOrigins(client, id, origins));
}

/** The GitHub integration (GH-01). Every mutation refreshes it and its repositories. */
export function useGitHubIntegration({ enabled = true }: { enabled?: boolean } = {}) {
  const client = useApiClient();
  return useQuery({ queryKey: queryKeys.githubIntegration, queryFn: () => fetchGitHubIntegration(client), enabled });
}

export function useGitHubRepositories({ enabled = true }: { enabled?: boolean } = {}) {
  const client = useApiClient();
  return useQuery({
    queryKey: queryKeys.githubRepositories,
    queryFn: () => fetchGitHubRepositories(client),
    enabled,
    retry: false,
  });
}

function useGitHubMutation<TArgs, TResult>(fn: (client: ApiClient, args: TArgs) => Promise<TResult>) {
  const client = useApiClient();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (args: TArgs) => fn(client, args),
    onSettled: () => queryClient.invalidateQueries({ queryKey: queryKeys.githubIntegration }),
  });
}

export function useStartGitHubIntegration() {
  return useGitHubMutation((client, organization: string) => startGitHubIntegration(client, organization));
}

export function useCompleteGitHubApp() {
  return useGitHubMutation((client, { code, state }: { code: string; state: string }) => completeGitHubApp(client, code, state));
}

export function useCompleteGitHubInstallation() {
  return useGitHubMutation((client, installationId: number) => completeGitHubInstallation(client, installationId));
}

export function useUpdateGitHubIntegration() {
  return useGitHubMutation((client, body: UpdateGitHubIntegrationRequest) => updateGitHubIntegration(client, body));
}

export function useDisconnectGitHubIntegration() {
  return useGitHubMutation((client) => disconnectGitHubIntegration(client));
}

/**
 * GitHub issues of a conversation (GH-02). While one is being created it is asked again every
 * few seconds, in case the realtime connection is away: conversation.issues_changed refreshes it
 * otherwise.
 */
export function useConversationIssues(conversationId: string, { enabled = true }: { enabled?: boolean } = {}) {
  const client = useApiClient();
  return useQuery({
    queryKey: queryKeys.conversationIssues(conversationId),
    queryFn: () => fetchConversationIssues(client, conversationId),
    enabled,
    refetchInterval: (q) => (q.state.data?.items.some((i) => i.status === "creating") ? 5000 : false),
  });
}

/** The issue form filled from the templates; fetched anew every time the form opens. */
export function useIssueDraft(conversationId: string, { enabled = true }: { enabled?: boolean } = {}) {
  const client = useApiClient();
  return useQuery({
    queryKey: queryKeys.issueDraft(conversationId),
    queryFn: () => fetchIssueDraft(client, conversationId),
    enabled,
    gcTime: 0,
    retry: false,
  });
}

export function useOpenGitHubIssues({ enabled = true }: { enabled?: boolean } = {}) {
  const client = useApiClient();
  return useQuery({ queryKey: queryKeys.openGitHubIssues, queryFn: () => fetchOpenGitHubIssues(client), enabled });
}

/** An issue change refreshes the issues, the conversation (awaiting_fix) and its feed. */
function useIssueMutation<TArgs, TResult>(conversationId: string, fn: (client: ApiClient, args: TArgs) => Promise<TResult>) {
  const client = useApiClient();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (args: TArgs) => fn(client, args),
    onSettled: () =>
      Promise.all([
        queryClient.invalidateQueries({ queryKey: queryKeys.conversationIssues(conversationId) }),
        queryClient.invalidateQueries({ queryKey: queryKeys.conversation(conversationId) }),
        queryClient.invalidateQueries({ queryKey: queryKeys.conversationMessages(conversationId) }),
        queryClient.invalidateQueries({ queryKey: queryKeys.openGitHubIssues }),
      ]),
  });
}

export function useCreateConversationIssue(conversationId: string) {
  return useIssueMutation(conversationId, (client, body: CreateIssueRequest) => createConversationIssue(client, conversationId, body));
}

export function useLinkConversationIssue(conversationId: string) {
  return useIssueMutation(conversationId, (client, number: number) => linkConversationIssue(client, conversationId, number));
}

export function useRetryConversationIssue(conversationId: string) {
  return useIssueMutation(conversationId, (client, issueId: string) => retryConversationIssue(client, conversationId, issueId));
}

export function useDismissConversationIssue(conversationId: string) {
  return useIssueMutation(conversationId, (client, issueId: string) => dismissConversationIssue(client, conversationId, issueId));
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

/**
 * The signed link of an attachment, for showing a picture. Links live 15 minutes: a cached one is
 * used for 10, then a new one is asked for.
 */
export function useAttachmentLink(id: string, { enabled = true }: { enabled?: boolean } = {}) {
  const client = useApiClient();
  return useQuery({
    queryKey: queryKeys.attachmentLink(id),
    queryFn: () => fetchAttachmentLink(client, id),
    enabled,
    staleTime: 10 * 60_000,
    gcTime: 10 * 60_000,
    refetchOnWindowFocus: false,
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
