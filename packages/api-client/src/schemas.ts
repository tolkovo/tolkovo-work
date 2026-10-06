import { z } from "zod";

import type { components } from "./schema.gen";

/**
 * Runtime checks of API answers at the boundary (docs/00 §7.1). Each schema is tied to the
 * generated type with `satisfies`: when api/openapi.yaml adds a required field or changes a
 * type, `make gen` regenerates the type and these lines stop compiling until they match.
 * Unknown fields are stripped, so additive server changes never break the panel.
 */
type Schemas = components["schemas"];

export const localeSchema = z.enum(["ru", "en"]) satisfies z.ZodType<Schemas["Locale"]>;
export const roleSchema = z.enum(["owner", "admin", "operator"]) satisfies z.ZodType<Schemas["Role"]>;

export const problemSchema = z.object({
  type: z.string(),
  title: z.string(),
  status: z.number(),
  code: z.string(),
  detail: z.string().optional(),
  instance: z.string().optional(),
  request_id: z.string().optional(),
}) satisfies z.ZodType<Schemas["Problem"]>;

export const userSchema = z.object({
  id: z.string(),
  email: z.string(),
  name: z.string(),
  locale: localeSchema,
  email_verified: z.boolean(),
}) satisfies z.ZodType<Schemas["User"]>;

export const workspaceSchema = z.object({
  id: z.string(),
  slug: z.string(),
  name: z.string(),
  locale: localeSchema,
  timezone: z.string(),
}) satisfies z.ZodType<Schemas["Workspace"]>;

export const sessionSchema = z.object({
  user: userSchema,
  workspace: workspaceSchema,
  role: roleSchema,
  permissions: z.array(z.string()),
  csrf_token: z.string().min(1),
}) satisfies z.ZodType<Schemas["Session"]>;

/** GET /auth/registration: whether the server takes new accounts. */
export const registrationStateSchema = z.object({ open: z.boolean() });

export const workspaceListSchema = z.object({
  items: z.array(
    z.object({
      id: z.string(),
      slug: z.string(),
      name: z.string(),
      role: roleSchema,
    }) satisfies z.ZodType<Schemas["WorkspaceSummary"]>,
  ),
}) satisfies z.ZodType<Schemas["WorkspaceList"]>;

export const presenceSchema = z.enum(["online", "away", "offline"]) satisfies z.ZodType<Schemas["Presence"]>;

export const memberListSchema = z.object({
  items: z.array(
    z.object({
      user_id: z.string(),
      email: z.string(),
      name: z.string(),
      role: roleSchema,
      active: z.boolean(),
      presence: presenceSchema,
    }) satisfies z.ZodType<Schemas["Member"]>,
  ),
}) satisfies z.ZodType<Schemas["MemberList"]>;

export const myPresenceSchema = z.object({
  status: z.enum(["online", "away"]),
}) satisfies z.ZodType<Schemas["MyPresence"]>;

export const routingSettingsSchema = z.object({
  mode: z.enum(["manual", "round_robin"]),
}) satisfies z.ZodType<Schemas["RoutingSettings"]>;

const aiProviderSchema = z.enum(["anthropic", "openai", "deepseek", "openai_compatible"]) satisfies z.ZodType<Schemas["AIProvider"]>;
const aiPricesSchema = z.object({
  input_micros: z.number(),
  output_micros: z.number(),
}) satisfies z.ZodType<Schemas["AIPrices"]>;

export const aiSettingsSchema = z.object({
  enabled: z.boolean(),
  provider: aiProviderSchema.nullable(),
  model: z.string().nullable(),
  base_url: z.string().nullable(),
  key: z.object({ hint: z.string(), set_at: z.string() }).nullable(),
  prices: aiPricesSchema,
  monthly_budget_micros: z.number().nullable(),
  spent_micros: z.number(),
  period_start: z.string(),
  models: z.array(
    z.object({ provider: aiProviderSchema, model: z.string(), prices: aiPricesSchema }) satisfies z.ZodType<Schemas["AIModel"]>,
  ),
  encryption_available: z.boolean(),
}) satisfies z.ZodType<Schemas["AISettings"]>;

export const aiCheckSchema = z.object({
  model: z.string(),
  latency_ms: z.number(),
}) satisfies z.ZodType<Schemas["AICheck"]>;

export const deviceSessionListSchema = z.object({
  items: z.array(
    z.object({
      id: z.string(),
      created_at: z.string(),
      last_seen_at: z.string(),
      user_agent: z.string(),
      current: z.boolean(),
    }) satisfies z.ZodType<Schemas["DeviceSession"]>,
  ),
}) satisfies z.ZodType<Schemas["DeviceSessionList"]>;

export const invitationSchema = z.object({
  id: z.string(),
  email: z.string(),
  role: roleSchema,
  invited_by_name: z.string(),
  expires_at: z.string(),
  created_at: z.string(),
  expired: z.boolean(),
}) satisfies z.ZodType<Schemas["Invitation"]>;

export const invitationListSchema = z.object({
  items: z.array(invitationSchema),
  email_enabled: z.boolean(),
}) satisfies z.ZodType<Schemas["InvitationList"]>;

export const invitationSentSchema = z.object({
  invitation: invitationSchema,
  invite_url: z.string().optional(),
}) satisfies z.ZodType<Schemas["InvitationSent"]>;

export const invitationPreviewSchema = z.object({
  workspace_name: z.string(),
  email: z.string(),
  role: roleSchema,
  expires_at: z.string(),
  account_exists: z.boolean(),
}) satisfies z.ZodType<Schemas["InvitationPreview"]>;

export const conversationStatusSchema = z.enum([
  "new",
  "bot",
  "queued",
  "assigned",
  "pending",
  "resolved",
  "closed",
]) satisfies z.ZodType<Schemas["ConversationStatus"]>;

const senderTypeSchema = z.enum(["contact", "operator", "bot", "system"]);

export const conversationPreviewSchema = z.object({
  sender_type: senderTypeSchema,
  kind: z.enum(["text", "media"]),
  text: z.string(),
  attachments: z.number(),
  created_at: z.string(),
}) satisfies z.ZodType<Schemas["ConversationPreview"]>;

export const conversationRatingSchema = z.object({
  score: z.number(),
  comment: z.string().nullable(),
  resolved_by: z.enum(["human", "ai"]),
  rated_at: z.string(),
}) satisfies z.ZodType<Schemas["ConversationRating"]>;

export const conversationSchema = z.object({
  id: z.string(),
  number: z.number(),
  status: conversationStatusSchema,
  priority: z.number(),
  line_level: z.number(),
  contact: z.object({ id: z.string(), name: z.string() }) satisfies z.ZodType<Schemas["ContactRef"]>,
  assignee_id: z.string().nullable(),
  channel_id: z.string().nullable(),
  subject: z.string().nullable(),
  first_response_at: z.string().nullable(),
  last_message_at: z.string(),
  resolved_at: z.string().nullable(),
  closed_at: z.string().nullable(),
  awaiting_fix: z.boolean(),
  version: z.number(),
  created_at: z.string(),
  updated_at: z.string(),
  last_message: conversationPreviewSchema.nullable(),
  tag_ids: z.array(z.string()),
  last_read_message_id: z.string().nullable(),
  unread_count: z.number(),
  rating: conversationRatingSchema.nullable(),
}) satisfies z.ZodType<Schemas["Conversation"]>;

export const contactSchema = z.object({
  id: z.string(),
  name: z.string(),
  name_placeholder: z.boolean(),
  email: z.string().nullable(),
  phone: z.string().nullable(),
  locale: z.string().nullable(),
  identity: z.enum(["verified", "unverified", "none"]),
  created_at: z.string(),
  updated_at: z.string(),
}) satisfies z.ZodType<Schemas["Contact"]>;

export const conversationPageSchema = z.object({
  items: z.array(conversationSchema),
  next_cursor: z.string().nullable(),
}) satisfies z.ZodType<Schemas["ConversationPage"]>;

export const inboxCountsSchema = z.object({
  mine: z.number(),
  mine_unread: z.number(),
  unassigned: z.number(),
  open: z.number(),
}) satisfies z.ZodType<Schemas["InboxCounts"]>;

export const attachmentSchema = z.object({
  id: z.string(),
  file_name: z.string(),
  content_type: z.string(),
  size: z.number(),
  status: z.enum(["uploading", "scanning", "ready", "rejected", "expired"]),
}) satisfies z.ZodType<Schemas["Attachment"]>;

export const messageEventSchema = z.object({
  type: z.enum([
    "status_changed",
    "assigned",
    "issue_created",
    "issue_linked",
    "issue_closed",
    "issue_reopened",
    "task_created",
    "task_linked",
    "task_unlinked",
    "task_closed",
    "task_reopened",
  ]),
  from: conversationStatusSchema,
  to: conversationStatusSchema,
  assignee_id: z.string().nullable(),
  previous_assignee_id: z.string().nullable(),
  issue: z
    .object({
      link_id: z.string(),
      repository: z.string(),
      number: z.number(),
      url: z.string(),
      reason: z.enum(["completed", "not_planned"]).optional(),
    })
    .optional() satisfies z.ZodType<Schemas["MessageEventIssue"] | undefined>,
  task: z
    .object({ task_id: z.string(), number: z.number(), reason: z.enum(["done", "canceled"]).optional() })
    .optional() satisfies z.ZodType<Schemas["MessageEventTask"] | undefined>,
}) satisfies z.ZodType<Schemas["MessageEvent"]>;

export const signedRequestSchema = z.object({
  method: z.string(),
  url: z.string(),
  headers: z.record(z.string(), z.string()),
  expires_at: z.string(),
}) satisfies z.ZodType<Schemas["SignedRequest"]>;

export const messageReportSchema = z.object({
  report_id: z.string(),
  app: z.object({
    name: z.string().optional(),
    version: z.string().optional(),
    os: z.string().optional(),
    os_version: z.string().optional(),
    locale: z.string().optional(),
    extra: z.record(z.string(), z.string()).optional(),
  }),
  contact_verified: z.boolean(),
  files: z.array(z.enum(["ok", "type_mismatch", "infected", "not_scanned", "missing"])),
}) satisfies z.ZodType<Schemas["MessageReport"]>;

export const messageSchema = z.object({
  id: z.string(),
  conversation_id: z.string(),
  direction: z.enum(["in", "out"]),
  sender_type: senderTypeSchema,
  sender_id: z.string().nullable(),
  kind: z.enum(["text", "media", "note", "event"]),
  body: z.string(),
  is_private: z.boolean(),
  delivery_status: z.string().nullable(),
  delivery_error: z
    .enum(["blocked", "chat_not_found", "limited", "rejected", "channel_unavailable", "failed"])
    .optional(),
  attachments: z.array(attachmentSchema),
  event: messageEventSchema.nullable(),
  unsupported: z.enum(["sticker", "location", "contact", "poll", "other"]).optional(),
  sent_outside_panel: z.boolean().optional(),
  report: messageReportSchema.optional(),
  created_at: z.string(),
}) satisfies z.ZodType<Schemas["Message"]>;

export const messagePageSchema = z.object({
  items: z.array(messageSchema),
  next_cursor: z.string().nullable(),
}) satisfies z.ZodType<Schemas["MessagePage"]>;

export const tagColorSchema = z.enum(["gray", "teal", "amber", "red", "violet", "blue", "green"]) satisfies z.ZodType<
  Schemas["TagColor"]
>;

export const tagSchema = z.object({
  id: z.string(),
  name: z.string(),
  color: tagColorSchema,
}) satisfies z.ZodType<Schemas["Tag"]>;

export const tagListSchema = z.object({ items: z.array(tagSchema) }) satisfies z.ZodType<Schemas["TagList"]>;

export const appLimitsSchema = z.object({
  max_file_mb: z.number(),
  max_files: z.number(),
  max_report_mb: z.number(),
  retention_days: z.number(),
  daily_reports: z.number(),
  daily_mb: z.number(),
}) satisfies z.ZodType<Schemas["AppLimits"]>;

export const widgetAnalyticsSchema = z.object({
  data_layer: z.boolean(),
  gtag: z.boolean(),
  metrika_counter: z.string().nullable(),
  goals: z.object({ chat_started: z.string(), contact_collected: z.string(), rated: z.string() }) satisfies z.ZodType<Schemas["WidgetGoals"]>,
}) satisfies z.ZodType<Schemas["WidgetAnalytics"]>;

export const channelSchema = z.object({
  id: z.string(),
  type: z.enum(["telegram_bot", "telegram_user", "app", "widget"]),
  name: z.string(),
  status: z.enum(["pending", "active", "error"]),
  error_code: z.string().nullable(),
  details: z.object({
    username: z.string().nullable(),
    display_name: z.string().nullable(),
    phone_masked: z.string().nullable(),
    delivery: z.enum(["webhook", "polling"]).optional(),
    app_limits: appLimitsSchema.optional(),
    widget_key: z.string().optional(),
    allowed_origins: z.array(z.string()).optional(),
    analytics: widgetAnalyticsSchema.optional(),
  }),
  last_inbound_at: z.string().nullable(),
  created_at: z.string(),
}) satisfies z.ZodType<Schemas["Channel"]>;

export const channelListSchema = z.object({
  items: z.array(channelSchema),
  telegram_user_available: z.boolean(),
  encryption_available: z.boolean(),
}) satisfies z.ZodType<Schemas["ChannelList"]>;

export const ingestKeySchema = z.object({
  id: z.string(),
  name: z.string(),
  prefix: z.string(),
  created_at: z.string(),
  last_used_at: z.string().nullable(),
  revoked_at: z.string().nullable(),
}) satisfies z.ZodType<Schemas["IngestKey"]>;

export const ingestKeyListSchema = z.object({ items: z.array(ingestKeySchema) }) satisfies z.ZodType<
  Schemas["IngestKeyList"]
>;

export const ingestKeyCreatedSchema = z.object({
  key: ingestKeySchema,
  secret: z.string(),
}) satisfies z.ZodType<Schemas["IngestKeyCreated"]>;

export const identitySecretSchema = z.object({ identity_secret: z.string() }) satisfies z.ZodType<
  Schemas["IdentitySecret"]
>;

export const appChannelCreatedSchema = z.object({
  channel: channelSchema,
  ingest_key: ingestKeyCreatedSchema,
  identity_secret: z.string(),
}) satisfies z.ZodType<Schemas["AppChannelCreated"]>;

export const widgetChannelCreatedSchema = z.object({
  channel: channelSchema,
  identity_secret: z.string(),
}) satisfies z.ZodType<Schemas["WidgetChannelCreated"]>;

export const gitHubTemplatesSchema = z.object({
  issue_title: z.string(),
  issue_body: z.string(),
  reply_ru: z.string(),
  reply_en: z.string(),
}) satisfies z.ZodType<Schemas["GitHubTemplates"]>;

export const gitHubRepositorySchema = z.object({
  id: z.number(),
  full_name: z.string(),
  private: z.boolean(),
  url: z.string(),
}) satisfies z.ZodType<Schemas["GitHubRepository"]>;

export const gitHubIntegrationSchema = z.object({
  id: z.string(),
  status: z.enum(["app_pending", "install_pending", "active"]),
  app: z.object({ id: z.number(), slug: z.string(), owner: z.string(), url: z.string() }).nullable(),
  install_url: z.string().nullable(),
  account: z.string().nullable(),
  repository: gitHubRepositorySchema.nullable(),
  labels: z.array(z.string()),
  templates: gitHubTemplatesSchema,
  custom_templates: z.object({
    issue_title: z.boolean(),
    issue_body: z.boolean(),
    reply_ru: z.boolean(),
    reply_en: z.boolean(),
  }),
  created_at: z.string(),
  updated_at: z.string(),
}) satisfies z.ZodType<Schemas["GitHubIntegration"]>;

export const gitHubIntegrationStateSchema = z.object({
  integration: gitHubIntegrationSchema.nullable(),
  defaults: gitHubTemplatesSchema,
  placeholders: z.object({ issue: z.array(z.string()), reply: z.array(z.string()) }),
  encryption_available: z.boolean(),
}) satisfies z.ZodType<Schemas["GitHubIntegrationState"]>;

export const gitHubManifestStartSchema = z.object({
  integration: gitHubIntegrationSchema,
  post_url: z.string(),
  manifest: z.string(),
}) satisfies z.ZodType<Schemas["GitHubManifestStart"]>;

export const gitHubRepositoryListSchema = z.object({
  items: z.array(gitHubRepositorySchema),
  more: z.boolean(),
}) satisfies z.ZodType<Schemas["GitHubRepositoryList"]>;

export const issueLinkSchema = z.object({
  id: z.string(),
  conversation_id: z.string(),
  repository: z.string(),
  number: z.number().nullable(),
  url: z.string().nullable(),
  title: z.string(),
  status: z.enum(["creating", "open", "closed", "failed"]),
  error: z.enum(["repository_unreachable", "app_refused", "rejected", "unavailable", "disconnected"]).optional(),
  created_by: z.string().nullable(),
  created_at: z.string(),
  updated_at: z.string(),
}) satisfies z.ZodType<Schemas["IssueLink"]>;

export const conversationIssuesSchema = z.object({
  repository: gitHubRepositorySchema.nullable(),
  items: z.array(issueLinkSchema),
  reply_draft: z.object({ issue_id: z.string(), number: z.number(), text: z.string() }).nullable(),
}) satisfies z.ZodType<Schemas["ConversationIssues"]>;

export const issueDraftSchema = z.object({
  repository: gitHubRepositorySchema,
  title: z.string(),
  body: z.string(),
  quote: z.string(),
  labels: z.array(z.string()),
  drafted_by: z.enum(["ai", "template"]),
  ai_fallback: z.enum(["budget_exceeded", "failed"]).optional(),
}) satisfies z.ZodType<Schemas["IssueDraft"]>;

export const openGitHubIssueListSchema = z.object({
  items: z.array(z.object({ repository: z.string(), number: z.number(), title: z.string(), url: z.string() })),
}) satisfies z.ZodType<Schemas["OpenGitHubIssueList"]>;

export const telegramLoginSchema = z.object({
  channel: channelSchema,
  next: z.enum(["code", "password", "done"]),
  code_via: z.enum(["app", "sms", "call", "other"]).optional(),
}) satisfies z.ZodType<Schemas["TelegramLogin"]>;

export const notificationSettingsSchema = z.object({
  email_missed: z.boolean(),
  email_delay_minutes: z.union([z.literal(5), z.literal(15), z.literal(30), z.literal(60)]),
  email_available: z.boolean(),
}) satisfies z.ZodType<Schemas["NotificationSettings"]>;

/** The tasks section (ADR-0015). */
export const taskStatusSchema = z.enum(["open", "in_progress", "done", "canceled"]) satisfies z.ZodType<Schemas["TaskStatus"]>;
export const taskPrioritySchema = z.enum(["low", "normal", "high", "urgent"]) satisfies z.ZodType<Schemas["TaskPriority"]>;
export const taskButtonSchema = z.enum(["tasks", "github", "both"]) satisfies z.ZodType<Schemas["TaskButton"]>;

export const taskSchema = z.object({
  id: z.string(),
  number: z.number(),
  queue_id: z.string().nullable(),
  title: z.string(),
  body: z.string(),
  status: taskStatusSchema,
  priority: taskPrioritySchema,
  assignee_id: z.string().nullable(),
  created_by: z.string().nullable(),
  created_at: z.string(),
  updated_at: z.string(),
  closed_at: z.string().nullable(),
  version: z.number(),
  conversation_ids: z.array(z.string()),
}) satisfies z.ZodType<Schemas["Task"]>;

export const taskPageSchema = z.object({
  items: z.array(taskSchema),
  next_cursor: z.string().nullable(),
}) satisfies z.ZodType<Schemas["TaskPage"]>;

export const taskSummarySchema = z.object({ assigned_open: z.number() }) satisfies z.ZodType<Schemas["TaskSummary"]>;

export const taskActivitySchema = z.object({
  id: z.string(),
  actor_id: z.string().nullable(),
  kind: z.enum(["comment", "created", "status", "assignee", "queue", "priority", "edited", "linked", "unlinked"]),
  body: z.string().optional(),
  from: z.string().optional(),
  to: z.string().optional(),
  fields: z.array(z.enum(["title", "body"])).optional(),
  conversation_id: z.string().optional(),
  created_at: z.string(),
}) satisfies z.ZodType<Schemas["TaskActivity"]>;

export const taskActivityListSchema = z.object({ items: z.array(taskActivitySchema) }) satisfies z.ZodType<Schemas["TaskActivityList"]>;

export const taskQueueSchema = z.object({
  id: z.string(),
  name: z.string(),
  member_ids: z.array(z.string()),
  open_tasks: z.number(),
}) satisfies z.ZodType<Schemas["TaskQueue"]>;

export const taskQueueListSchema = z.object({ items: z.array(taskQueueSchema) }) satisfies z.ZodType<Schemas["TaskQueueList"]>;

export const taskSettingsSchema = z.object({
  enabled: z.boolean(),
  conversation_button: taskButtonSchema,
  reply_ru: z.string(),
  reply_en: z.string(),
}) satisfies z.ZodType<Schemas["TaskSettings"]>;

export const taskSettingsViewSchema = z.object({
  settings: taskSettingsSchema,
  defaults: z.object({ reply_ru: z.string(), reply_en: z.string() }),
  placeholders: z.array(z.string()),
}) satisfies z.ZodType<Schemas["TaskSettingsView"]>;

export const conversationTasksSchema = z.object({
  items: z.array(taskSchema),
  settings: z.object({ enabled: z.boolean(), conversation_button: taskButtonSchema }),
  reply_draft: z.object({ task_id: z.string(), number: z.number(), text: z.string() }).nullable(),
}) satisfies z.ZodType<Schemas["ConversationTasks"]>;

export const taskDraftSchema = z.object({
  title: z.string(),
  body: z.string(),
  drafted_by: z.enum(["ai", "template"]),
  ai_fallback: z.enum(["budget_exceeded", "failed"]).optional(),
}) satisfies z.ZodType<Schemas["TaskDraft"]>;

/** Telegram notifications of members (TK-02). */
export const myTelegramSchema = z.object({
  bot_username: z.string().nullable(),
  linked: z.boolean(),
  name: z.string().nullable(),
  linked_at: z.string().nullable(),
  pending: z.boolean(),
}) satisfies z.ZodType<Schemas["MyTelegram"]>;

export const telegramLinkStartSchema = z.object({ url: z.string(), expires_at: z.string() }) satisfies z.ZodType<
  Schemas["TelegramLinkStart"]
>;

export const telegramBotStateSchema = z.object({
  bot: z.object({ username: z.string() }).nullable(),
  encryption_available: z.boolean(),
}) satisfies z.ZodType<Schemas["TelegramBotState"]>;
