import { parseAnswer, unwrap, type ApiClient } from "./client";
import { isApiError } from "./errors";
import {
  registrationStateSchema,
  appChannelCreatedSchema,
  channelListSchema,
  channelSchema,
  identitySecretSchema,
  ingestKeyCreatedSchema,
  ingestKeyListSchema,
  contactSchema,
  conversationPageSchema,
  conversationIssuesSchema,
  conversationSchema,
  deviceSessionListSchema,
  gitHubIntegrationSchema,
  gitHubIntegrationStateSchema,
  gitHubManifestStartSchema,
  gitHubRepositoryListSchema,
  inboxCountsSchema,
  issueDraftSchema,
  issueLinkSchema,
  notificationSettingsSchema,
  invitationListSchema,
  invitationPreviewSchema,
  invitationSentSchema,
  memberListSchema,
  myPresenceSchema,
  routingSettingsSchema,
  aiCheckSchema,
  aiSettingsSchema,
  messagePageSchema,
  messageSchema,
  openGitHubIssueListSchema,
  sessionSchema,
  signedRequestSchema,
  tagListSchema,
  tagSchema,
  telegramLoginSchema,
  widgetChannelCreatedSchema,
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
export type Contact = Schemas["Contact"];
export type ContactInput = Schemas["ContactInput"];
export type Conversation = Schemas["Conversation"];
export type ConversationStatus = Schemas["ConversationStatus"];
export type ConversationPreview = Schemas["ConversationPreview"];
export type ConversationPage = Schemas["ConversationPage"];
export type InboxCounts = Schemas["InboxCounts"];
export type NotificationSettings = Schemas["NotificationSettings"];
export type NotificationSettingsInput = Schemas["UpdateNotificationSettingsRequest"];
export type EmailDelayMinutes = Schemas["EmailDelayMinutes"];
export type Presence = Schemas["Presence"];
export type MyPresence = Schemas["MyPresence"];
export type OperatorStatus = MyPresence["status"];
export type RoutingSettings = Schemas["RoutingSettings"];
export type RoutingMode = RoutingSettings["mode"];
export type AIProvider = Schemas["AIProvider"];
export type AISettings = Schemas["AISettings"];
export type AISettingsUpdate = Schemas["AISettingsUpdate"];
export type AICheck = Schemas["AICheck"];
export type Channel = Schemas["Channel"];
export type ChannelList = Schemas["ChannelList"];
export type TelegramLogin = Schemas["TelegramLogin"];
export type ConnectTelegramBotRequest = Schemas["ConnectTelegramBotRequest"];
export type AppLimits = Schemas["AppLimits"];
export type WidgetAnalytics = Schemas["WidgetAnalytics"];
export type ConversationRating = Schemas["ConversationRating"];
export type AppChannelCreated = Schemas["AppChannelCreated"];
export type WidgetChannelCreated = Schemas["WidgetChannelCreated"];
export type IngestKey = Schemas["IngestKey"];
export type GitHubIntegration = Schemas["GitHubIntegration"];
export type GitHubIntegrationState = Schemas["GitHubIntegrationState"];
export type GitHubManifestStart = Schemas["GitHubManifestStart"];
export type GitHubRepository = Schemas["GitHubRepository"];
export type GitHubRepositoryList = Schemas["GitHubRepositoryList"];
export type GitHubTemplates = Schemas["GitHubTemplates"];
export type UpdateGitHubIntegrationRequest = Schemas["UpdateGitHubIntegrationRequest"];
export type IssueLink = Schemas["IssueLink"];
export type ConversationIssues = Schemas["ConversationIssues"];
export type IssueDraft = Schemas["IssueDraft"];
export type CreateIssueRequest = Schemas["CreateIssueRequest"];
export type OpenGitHubIssue = Schemas["OpenGitHubIssue"];
export type IngestKeyCreated = Schemas["IngestKeyCreated"];
export type Message = Schemas["Message"];
export type MessageReport = Schemas["MessageReport"];
export type Attachment = Schemas["Attachment"];
export type SignedRequest = Schemas["SignedRequest"];
export type MessageEvent = Schemas["MessageEvent"];
export type MessagePage = Schemas["MessagePage"];
export type Tag = Schemas["Tag"];
export type TagColor = Schemas["TagColor"];
export type CreateTagRequest = Schemas["CreateTagRequest"];
export type PostMessageRequest = Schemas["PostMessageRequest"];

/**
 * Filter of GET /conversations: any of the statuses, `me` or `none` (unassigned), and one
 * contact's conversations (the history in the customer card).
 */
export type ConversationFilter = {
  status?: ConversationStatus[];
  assignee?: "me" | "none";
  contact_id?: string;
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

/** GET /auth/registration: `true` when "Create workspace" would be accepted. */
export async function fetchRegistrationOpen(client: ApiClient): Promise<boolean> {
  return parseAnswer(registrationStateSchema, await unwrap(client.GET("/auth/registration"))).open;
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

/**
 * A reply or an internal note. The same Idempotency-Key on a retry returns the first message
 * instead of posting twice (docs/00 §8), so keep the key until the send succeeds.
 */
export async function postMessage(
  client: ApiClient,
  id: string,
  body: PostMessageRequest,
  idempotencyKey: string,
): Promise<Message> {
  const data = await unwrap(
    client.POST("/conversations/{id}/messages", {
      params: { path: { id }, header: { "Idempotency-Key": idempotencyKey } },
      body,
    }),
  );
  return parseAnswer(messageSchema, data, 201);
}

/** Sends a reply that failed to deliver again (`409 not_failed` for any other). */
export async function retryMessageDelivery(client: ApiClient, conversationId: string, messageId: string): Promise<Message> {
  const data = await unwrap(
    client.POST("/conversations/{id}/messages/{message_id}/retry", {
      params: { path: { id: conversationId, message_id: messageId } },
    }),
  );
  return parseAnswer(messageSchema, data);
}

/**
 * A signed GET for a sent attachment, valid 15 minutes. The file always comes as an attachment
 * (`Content-Disposition`), so following the link downloads it; an `<img>` shows a picture.
 */
export async function fetchAttachmentLink(client: ApiClient, id: string): Promise<SignedRequest> {
  return parseAnswer(
    signedRequestSchema,
    await unwrap(client.GET("/attachments/{id}/download-link", { params: { path: { id } } })),
  );
}

/** Moves the conversation along the state machine; `version` guards against concurrent changes. */
export async function changeConversationStatus(
  client: ApiClient,
  id: string,
  status: ConversationStatus,
  version?: number,
): Promise<Conversation> {
  return parseAnswer(
    conversationSchema,
    await unwrap(
      client.POST("/conversations/{id}/status", {
        params: { path: { id } },
        body: version === undefined ? { status } : { status, version },
      }),
    ),
  );
}

/** Takes, transfers or (with `null`) releases the conversation. */
export async function assignConversation(
  client: ApiClient,
  id: string,
  assigneeId: string | null,
  version?: number,
): Promise<Conversation> {
  return parseAnswer(
    conversationSchema,
    await unwrap(
      client.PUT("/conversations/{id}/assignee", {
        params: { path: { id } },
        body: version === undefined ? { assignee_id: assigneeId } : { assignee_id: assigneeId, version },
      }),
    ),
  );
}

/** Replaces the tags of the conversation. */
export async function setConversationTags(
  client: ApiClient,
  id: string,
  tagIds: string[],
  version?: number,
): Promise<Conversation> {
  return parseAnswer(
    conversationSchema,
    await unwrap(
      client.PUT("/conversations/{id}/tags", {
        params: { path: { id } },
        body: version === undefined ? { tag_ids: tagIds } : { tag_ids: tagIds, version },
      }),
    ),
  );
}

/** Moves the caller's read mark up to the message; never back. */
export async function markConversationRead(client: ApiClient, id: string, upToMessageId: string): Promise<void> {
  await unwrap(
    client.POST("/conversations/{id}/read", { params: { path: { id } }, body: { up_to_message_id: upToMessageId } }),
  );
}

export async function fetchTags(client: ApiClient): Promise<Tag[]> {
  return parseAnswer(tagListSchema, await unwrap(client.GET("/tags"))).items;
}

export async function createTag(client: ApiClient, body: CreateTagRequest): Promise<Tag> {
  return parseAnswer(tagSchema, await unwrap(client.POST("/tags", { body })), 201);
}

export async function fetchContact(client: ApiClient, id: string): Promise<Contact> {
  return parseAnswer(contactSchema, await unwrap(client.GET("/contacts/{id}", { params: { path: { id } } })));
}

/** Changes the given fields; an empty email, phone or locale clears it. */
export async function updateContact(client: ApiClient, id: string, body: ContactInput): Promise<Contact> {
  return parseAnswer(contactSchema, await unwrap(client.PATCH("/contacts/{id}", { params: { path: { id } }, body })));
}

/** What the server does for the caller in this workspace: the email about missed messages. */
export async function fetchNotificationSettings(client: ApiClient): Promise<NotificationSettings> {
  return parseAnswer(notificationSettingsSchema, await unwrap(client.GET("/me/notifications")));
}

export async function updateNotificationSettings(
  client: ApiClient,
  body: NotificationSettingsInput,
): Promise<NotificationSettings> {
  return parseAnswer(notificationSettingsSchema, await unwrap(client.PATCH("/me/notifications", { body })));
}

/** Whether the caller accepts conversations in this workspace (Q-02). */
export async function fetchMyPresence(client: ApiClient): Promise<MyPresence> {
  return parseAnswer(myPresenceSchema, await unwrap(client.GET("/me/presence")));
}

export async function setMyPresence(client: ApiClient, status: OperatorStatus): Promise<MyPresence> {
  return parseAnswer(myPresenceSchema, await unwrap(client.PUT("/me/presence", { body: { status } })));
}

/** How the workspace distributes its queue (Q-01). */
export async function fetchRouting(client: ApiClient): Promise<RoutingSettings> {
  return parseAnswer(routingSettingsSchema, await unwrap(client.GET("/routing")));
}

export async function setRouting(client: ApiClient, mode: RoutingMode): Promise<RoutingSettings> {
  return parseAnswer(routingSettingsSchema, await unwrap(client.PUT("/routing", { body: { mode } })));
}

/** The workspace's AI: provider, model, key hint, prices, budget and the month's spending (AI-01). */
export async function fetchAISettings(client: ApiClient): Promise<AISettings> {
  return parseAnswer(aiSettingsSchema, await unwrap(client.GET("/ai/settings")));
}

/** Replaces the AI settings; `api_key` left out keeps the saved key. */
export async function setAISettings(client: ApiClient, body: AISettingsUpdate): Promise<AISettings> {
  return parseAnswer(aiSettingsSchema, await unwrap(client.PUT("/ai/settings", { body })));
}

/** Sends a tiny request with the saved settings. */
export async function checkAISettings(client: ApiClient): Promise<AICheck> {
  return parseAnswer(aiCheckSchema, await unwrap(client.POST("/ai/settings/check")));
}

/** Channels of the workspace and what this server can connect (docs/02 §11, ADR-0014). */
export async function fetchChannels(client: ApiClient): Promise<ChannelList> {
  return parseAnswer(channelListSchema, await unwrap(client.GET("/channels")));
}

/** Connects a bot by its token from @BotFather. */
export async function connectTelegramBot(client: ApiClient, body: ConnectTelegramBotRequest): Promise<Channel> {
  return parseAnswer(channelSchema, await unwrap(client.POST("/channels/telegram-bot", { body })), 201);
}

/** Starts connecting a personal account: Telegram sends a login code to it. */
export async function startTelegramUserLogin(client: ApiClient, phone: string): Promise<TelegramLogin> {
  return parseAnswer(telegramLoginSchema, await unwrap(client.POST("/channels/telegram-user", { body: { phone } })), 201);
}

export async function submitTelegramUserCode(client: ApiClient, id: string, code: string): Promise<TelegramLogin> {
  return parseAnswer(
    telegramLoginSchema,
    await unwrap(client.POST("/channels/{id}/telegram-user/code", { params: { path: { id } }, body: { code } })),
  );
}

/** The cloud password is used once by the server for Telegram's proof and never stored. */
export async function submitTelegramUserPassword(client: ApiClient, id: string, password: string): Promise<TelegramLogin> {
  return parseAnswer(
    telegramLoginSchema,
    await unwrap(client.POST("/channels/{id}/telegram-user/password", { params: { path: { id } }, body: { password } })),
  );
}

export async function renameChannel(client: ApiClient, id: string, name: string): Promise<Channel> {
  return parseAnswer(
    channelSchema,
    await unwrap(client.PATCH("/channels/{id}", { params: { path: { id } }, body: { name } })),
  );
}

/** Creates an app channel (docs/02 §11.6): its first ingest key and the identity secret come only here. */
export async function connectAppChannel(client: ApiClient, name: string): Promise<AppChannelCreated> {
  return parseAnswer(appChannelCreatedSchema, await unwrap(client.POST("/channels/app", { body: { name } })), 201);
}

export async function fetchIngestKeys(client: ApiClient, id: string): Promise<IngestKey[]> {
  const list = parseAnswer(ingestKeyListSchema, await unwrap(client.GET("/channels/{id}/ingest-keys", { params: { path: { id } } })));
  return list.items;
}

/** The key itself is in the answer only. */
export async function createIngestKey(client: ApiClient, id: string, name: string): Promise<IngestKeyCreated> {
  return parseAnswer(
    ingestKeyCreatedSchema,
    await unwrap(client.POST("/channels/{id}/ingest-keys", { params: { path: { id } }, body: { name } })),
    201,
  );
}

export async function revokeIngestKey(client: ApiClient, id: string, keyId: string): Promise<void> {
  await unwrap(client.DELETE("/channels/{id}/ingest-keys/{key_id}", { params: { path: { id, key_id: keyId } } }));
}

/** The old secret stops working at once; the new one is in the answer only. */
export async function rotateIdentitySecret(client: ApiClient, id: string): Promise<string> {
  const body = parseAnswer(
    identitySecretSchema,
    await unwrap(client.POST("/channels/{id}/identity-secret", { params: { path: { id } } })),
  );
  return body.identity_secret;
}

export async function setAppLimits(client: ApiClient, id: string, limits: AppLimits): Promise<Channel> {
  return parseAnswer(
    channelSchema,
    await unwrap(client.PUT("/channels/{id}/app-limits", { params: { path: { id } }, body: limits })),
  );
}

/** Creates a chat widget (docs/04 §6): the identity secret comes only here; the key is public. */
export async function connectWidgetChannel(
  client: ApiClient,
  body: { name: string; allowed_origins: string[] },
): Promise<WidgetChannelCreated> {
  return parseAnswer(widgetChannelCreatedSchema, await unwrap(client.POST("/channels/widget", { body })), 201);
}

/** Replaces the pages that may embed a widget. */
export async function setWidgetOrigins(client: ApiClient, id: string, origins: string[]): Promise<Channel> {
  return parseAnswer(
    channelSchema,
    await unwrap(client.PUT("/channels/{id}/widget-origins", { params: { path: { id } }, body: { allowed_origins: origins } })),
  );
}

/** Changes which site analytics a widget reports goals to (docs/04 §6.6). */
export async function setWidgetAnalytics(client: ApiClient, id: string, analytics: WidgetAnalytics): Promise<Channel> {
  return parseAnswer(
    channelSchema,
    await unwrap(client.PUT("/channels/{id}/widget-analytics", { params: { path: { id } }, body: analytics })),
  );
}

/** Disconnects a channel or cancels a login in progress. */
export async function disconnectChannel(client: ApiClient, id: string): Promise<void> {
  await unwrap(client.DELETE("/channels/{id}", { params: { path: { id } } }));
}

/** The workspace's GitHub integration (GH-01, ADR-0012); `integration` is null before a start. */
export async function fetchGitHubIntegration(client: ApiClient): Promise<GitHubIntegrationState> {
  return parseAnswer(gitHubIntegrationStateSchema, await unwrap(client.GET("/integrations/github")));
}

/** Step 1: the manifest the browser posts to GitHub as the form field `manifest`. */
export async function startGitHubIntegration(client: ApiClient, organization: string): Promise<GitHubManifestStart> {
  const body = organization ? { organization } : {};
  return parseAnswer(gitHubManifestStartSchema, await unwrap(client.POST("/integrations/github/manifest", { body })), 201);
}

/** Step 2: `code` and `state` from GitHub's redirect. */
export async function completeGitHubApp(client: ApiClient, code: string, state: string): Promise<GitHubIntegration> {
  return parseAnswer(gitHubIntegrationSchema, await unwrap(client.POST("/integrations/github/app", { body: { code, state } })));
}

/** Step 3: `installation_id` from GitHub's redirect. */
export async function completeGitHubInstallation(client: ApiClient, installationId: number): Promise<GitHubIntegration> {
  return parseAnswer(
    gitHubIntegrationSchema,
    await unwrap(client.POST("/integrations/github/installation", { body: { installation_id: installationId } })),
  );
}

export async function fetchGitHubRepositories(client: ApiClient): Promise<GitHubRepositoryList> {
  return parseAnswer(gitHubRepositoryListSchema, await unwrap(client.GET("/integrations/github/repositories")));
}

export async function updateGitHubIntegration(client: ApiClient, body: UpdateGitHubIntegrationRequest): Promise<GitHubIntegration> {
  return parseAnswer(gitHubIntegrationSchema, await unwrap(client.PATCH("/integrations/github", { body })));
}

/** Forgets the App's key; the App itself stays on GitHub. */
export async function disconnectGitHubIntegration(client: ApiClient): Promise<void> {
  await unwrap(client.DELETE("/integrations/github"));
}

/** Issues of a conversation (GH-02); `repository` is null when GitHub cannot take new ones. */
export async function fetchConversationIssues(client: ApiClient, conversationId: string): Promise<ConversationIssues> {
  const data = await unwrap(client.GET("/conversations/{id}/issues", { params: { path: { id: conversationId } } }));
  return parseAnswer(conversationIssuesSchema, data);
}

/** The issue form filled from the workspace's templates. */
export async function fetchIssueDraft(client: ApiClient, conversationId: string): Promise<IssueDraft> {
  const data = await unwrap(client.GET("/conversations/{id}/issues/draft", { params: { path: { id: conversationId } } }));
  return parseAnswer(issueDraftSchema, data);
}

/** Records the issue; the worker creates it on GitHub next. */
export async function createConversationIssue(client: ApiClient, conversationId: string, body: CreateIssueRequest): Promise<IssueLink> {
  const data = await unwrap(client.POST("/conversations/{id}/issues", { params: { path: { id: conversationId } }, body }));
  return parseAnswer(issueLinkSchema, data, 202);
}

/** Links the conversation to an issue of the workspace's repository by number. */
export async function linkConversationIssue(client: ApiClient, conversationId: string, number: number): Promise<IssueLink> {
  const data = await unwrap(
    client.POST("/conversations/{id}/issues/link", { params: { path: { id: conversationId } }, body: { number } }),
  );
  return parseAnswer(issueLinkSchema, data, 201);
}

/** Sends an issue GitHub refused again. */
export async function retryConversationIssue(client: ApiClient, conversationId: string, issueId: string): Promise<IssueLink> {
  const data = await unwrap(
    client.POST("/conversations/{id}/issues/{issue_id}/retry", { params: { path: { id: conversationId, issue_id: issueId } } }),
  );
  return parseAnswer(issueLinkSchema, data, 202);
}

/** Removes an issue GitHub refused. */
export async function dismissConversationIssue(client: ApiClient, conversationId: string, issueId: string): Promise<void> {
  await unwrap(client.DELETE("/conversations/{id}/issues/{issue_id}", { params: { path: { id: conversationId, issue_id: issueId } } }));
}

/** Open issues created or linked in the workspace, to link another conversation to. */
export async function fetchOpenGitHubIssues(client: ApiClient): Promise<OpenGitHubIssue[]> {
  return parseAnswer(openGitHubIssueListSchema, await unwrap(client.GET("/integrations/github/issues"))).items;
}

/** UI hint only: the server checks every permission again (docs/00 §9.2). */
export function hasPermission(session: Session | null | undefined, permission: string): boolean {
  return session?.permissions.includes(permission) ?? false;
}
