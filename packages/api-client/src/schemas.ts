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

export const memberListSchema = z.object({
  items: z.array(
    z.object({
      user_id: z.string(),
      email: z.string(),
      name: z.string(),
      role: roleSchema,
      active: z.boolean(),
    }) satisfies z.ZodType<Schemas["Member"]>,
  ),
}) satisfies z.ZodType<Schemas["MemberList"]>;

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
  version: z.number(),
  created_at: z.string(),
  updated_at: z.string(),
  last_message: conversationPreviewSchema.nullable(),
  tag_ids: z.array(z.string()),
  last_read_message_id: z.string().nullable(),
  unread_count: z.number(),
}) satisfies z.ZodType<Schemas["Conversation"]>;

export const contactSchema = z.object({
  id: z.string(),
  name: z.string(),
  email: z.string().nullable(),
  phone: z.string().nullable(),
  locale: z.string().nullable(),
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
  type: z.enum(["status_changed", "assigned"]),
  from: conversationStatusSchema,
  to: conversationStatusSchema,
  assignee_id: z.string().nullable(),
  previous_assignee_id: z.string().nullable(),
}) satisfies z.ZodType<Schemas["MessageEvent"]>;

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

export const channelSchema = z.object({
  id: z.string(),
  type: z.enum(["telegram_bot", "telegram_user"]),
  name: z.string(),
  status: z.enum(["pending", "active", "error"]),
  error_code: z.string().nullable(),
  details: z.object({
    username: z.string().nullable(),
    display_name: z.string().nullable(),
    phone_masked: z.string().nullable(),
    delivery: z.enum(["webhook", "polling"]).optional(),
  }),
  last_inbound_at: z.string().nullable(),
  created_at: z.string(),
}) satisfies z.ZodType<Schemas["Channel"]>;

export const channelListSchema = z.object({
  items: z.array(channelSchema),
  telegram_user_available: z.boolean(),
  encryption_available: z.boolean(),
}) satisfies z.ZodType<Schemas["ChannelList"]>;

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
