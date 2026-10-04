import type { Contact, Conversation, Message, Session } from "@tolkovo/api-client";

import { operatorSession } from "./fake-api";

/** Inbox fixtures shaped by the generated API types, so a contract change breaks the tests. */

/** An operator with the phase 1 operator permissions (docs/04 §4.3). */
export const inboxOperator: Session = {
  ...operatorSession,
  permissions: [
    "contacts.edit",
    "contacts.view",
    "conversations.note",
    "conversations.reply",
    "conversations.tag",
    "conversations.take",
    "conversations.view",
    "members.view",
  ],
};

export const conv = (id: string, patch: Partial<Conversation> = {}): Conversation => ({
  id,
  number: 7,
  status: "assigned",
  priority: 0,
  line_level: 1,
  contact: { id: `k-${id}`, name: "Марина Ковалёва" },
  assignee_id: operatorSession.user.id,
  channel_id: null,
  subject: "Двойное списание",
  first_response_at: null,
  last_message_at: new Date().toISOString(),
  resolved_at: null,
  closed_at: null,
  version: 1,
  created_at: "2026-10-04T12:00:00Z",
  updated_at: "2026-10-04T12:00:00Z",
  last_message: {
    sender_type: "contact",
    kind: "text",
    text: "Тут есть кто-нибудь?",
    attachments: 0,
    created_at: "2026-10-04T12:05:00Z",
  },
  tag_ids: [],
  last_read_message_id: null,
  unread_count: 0,
  ...patch,
});

export const msg = (id: string, patch: Partial<Message>): Message => ({
  id,
  conversation_id: "c1",
  direction: "in",
  sender_type: "contact",
  sender_id: "k-c1",
  kind: "text",
  body: "",
  is_private: false,
  delivery_status: null,
  attachments: [],
  event: null,
  created_at: "2026-10-04T12:01:00Z",
  ...patch,
});

export const contact = (id: string, patch: Partial<Contact> = {}): Contact => ({
  id,
  name: "Марина Ковалёва",
  email: "marina@example.com",
  phone: null,
  locale: "ru",
  created_at: "2025-03-12T09:00:00Z",
  updated_at: "2025-03-12T09:00:00Z",
  ...patch,
});
