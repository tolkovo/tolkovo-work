import { createContext, useContext, type ReactNode } from "react";

import type { ApiClient } from "./client";
import type { ConversationFilter } from "./endpoints";
import type { TaskBoardFilter, TaskFilter, TaskFlowFilter } from "./tasks";

// The API client of the app and the keys of the Query cache, shared by every hook module
// (react.tsx re-exports them).

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
  myAccessTokens: ["me", "access-tokens"] as const,
  accessTokens: ["access-tokens"] as const,
  myNotifications: ["me", "notifications"] as const,
  myPresence: ["me", "presence"] as const,
  routing: ["routing"] as const,
  routingMembers: ["routing", "members"] as const,
  aiSettings: ["ai", "settings"] as const,
  aiBot: ["ai", "bot"] as const,
  kbArticles: ["kb", "articles"] as const,
  kbArticle: (id: string) => ["kb", "articles", id] as const,
  kbSearch: (q: string, topic: string | null) => ["kb", "search", q, topic] as const,
  kbGuide: ["kb", "guide"] as const,
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
  /** Tasks of one conversation and the task form drafted from it (ADR-0015). */
  conversationTasks: (id: string) => ["conversations", "tasks", id] as const,
  taskDraft: (id: string) => ["conversations", "task-draft", id] as const,
  /** Root of the tasks section: task.changed invalidates everything under it. */
  tasks: ["tasks"] as const,
  taskList: (filter: TaskFilter) => ["tasks", "list", filter] as const,
  taskBoard: (filter: TaskBoardFilter) => ["tasks", "board", filter] as const,
  taskBoardSettings: ["tasks", "board-settings"] as const,
  taskFlow: (filter: TaskFlowFilter) => ["tasks", "flow", filter] as const,
  task: (id: string) => ["tasks", "detail", id] as const,
  taskActivity: (id: string) => ["tasks", "activity", id] as const,
  taskSummary: ["tasks", "summary"] as const,
  taskQueues: ["tasks", "queues"] as const,
  taskTags: ["tasks", "tags"] as const,
  taskSettings: ["tasks", "settings"] as const,
  taskTelegramBot: ["tasks", "telegram-bot"] as const,
  myTelegram: ["me", "telegram"] as const,
  contact: (id: string) => ["contacts", "detail", id] as const,
  tags: ["tags"] as const,
  topics: ["topics"] as const,
  attachmentLink: (id: string) => ["attachments", "link", id] as const,
};
