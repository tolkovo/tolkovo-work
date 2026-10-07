import { parseAnswer, unwrap, type ApiClient } from "./client";
import type { components } from "./schema.gen";
import {
  conversationTasksSchema,
  taskActivityListSchema,
  taskActivitySchema,
  taskDraftSchema,
  taskPageSchema,
  taskQueueListSchema,
  taskQueueSchema,
  signedRequestSchema,
  taskFileUploadSchema,
  taskSchema,
  taskSettingsViewSchema,
  taskSummarySchema,
  myTelegramSchema,
  telegramBotStateSchema,
  telegramLinkStartSchema,
} from "./schemas";

/** The tasks section (ADR-0015): a tracker for developers and the IT team next to GitHub issues. */

type Schemas = components["schemas"];

export type Task = Schemas["Task"];
export type TaskStatus = Schemas["TaskStatus"];
export type TaskPriority = Schemas["TaskPriority"];
export type TaskButton = Schemas["TaskButton"];
export type TaskPage = Schemas["TaskPage"];
export type TaskCreate = Schemas["TaskCreate"];
export type TaskUpdate = Schemas["TaskUpdate"];
export type TaskActivity = Schemas["TaskActivity"];
export type TaskQueue = Schemas["TaskQueue"];
export type TaskQueueInput = Schemas["TaskQueueInput"];
export type TaskSettings = Schemas["TaskSettings"];
export type TaskSettingsView = Schemas["TaskSettingsView"];
export type ConversationTasks = Schemas["ConversationTasks"];
export type TaskDraft = Schemas["TaskDraft"];
export type MyTelegram = Schemas["MyTelegram"];
export type TelegramLinkStart = Schemas["TelegramLinkStart"];
export type TelegramBotState = Schemas["TelegramBotState"];

/**
 * Filter of GET /tasks; `assignee` is `me`, `none` or a member id, `queue` and `topic` are
 * `none` or an id.
 */
export type TaskFilter = {
  state?: "open" | "closed";
  status?: TaskStatus;
  assignee?: string;
  queue?: string;
  topic?: string;
  q?: string;
};

export async function fetchTasks(client: ApiClient, filter: TaskFilter, cursor?: string): Promise<TaskPage> {
  const query = { ...filter, ...(cursor ? { cursor } : {}) };
  return parseAnswer(taskPageSchema, await unwrap(client.GET("/tasks", { params: { query } })));
}

export async function fetchTask(client: ApiClient, id: string): Promise<Task> {
  return parseAnswer(taskSchema, await unwrap(client.GET("/tasks/{id}", { params: { path: { id } } })));
}

export async function fetchTaskSummary(client: ApiClient): Promise<number> {
  return parseAnswer(taskSummarySchema, await unwrap(client.GET("/tasks/summary"))).assigned_open;
}

export async function createTask(client: ApiClient, body: TaskCreate): Promise<Task> {
  return parseAnswer(taskSchema, await unwrap(client.POST("/tasks", { body })), 201);
}

/** The whole changeable state, read at `version`; `409 task_conflict` when it changed since. */
export async function updateTask(client: ApiClient, id: string, body: TaskUpdate): Promise<Task> {
  return parseAnswer(taskSchema, await unwrap(client.PUT("/tasks/{id}", { params: { path: { id } }, body })));
}

export async function deleteTask(client: ApiClient, id: string): Promise<void> {
  await unwrap(client.DELETE("/tasks/{id}", { params: { path: { id } } }));
}

export async function fetchTaskActivity(client: ApiClient, id: string): Promise<TaskActivity[]> {
  return parseAnswer(taskActivityListSchema, await unwrap(client.GET("/tasks/{id}/activity", { params: { path: { id } } }))).items;
}

export async function commentTask(client: ApiClient, id: string, body: string, fileIds: string[] = []): Promise<TaskActivity> {
  const data = await unwrap(
    client.POST("/tasks/{id}/activity", { params: { path: { id } }, body: fileIds.length > 0 ? { body, file_ids: fileIds } : { body } }),
  );
  return parseAnswer(taskActivitySchema, data, 201);
}

export async function linkTaskConversation(client: ApiClient, id: string, conversationId: string): Promise<Task> {
  const data = await unwrap(client.POST("/tasks/{id}/conversations", { params: { path: { id } }, body: { conversation_id: conversationId } }));
  return parseAnswer(taskSchema, data, 201);
}

export async function unlinkTaskConversation(client: ApiClient, id: string, conversationId: string): Promise<void> {
  await unwrap(client.DELETE("/tasks/{id}/conversations/{conversation_id}", { params: { path: { id, conversation_id: conversationId } } }));
}

export async function fetchTaskQueues(client: ApiClient): Promise<TaskQueue[]> {
  return parseAnswer(taskQueueListSchema, await unwrap(client.GET("/task-queues"))).items;
}

export async function createTaskQueue(client: ApiClient, body: TaskQueueInput): Promise<TaskQueue> {
  return parseAnswer(taskQueueSchema, await unwrap(client.POST("/task-queues", { body })), 201);
}

export async function updateTaskQueue(client: ApiClient, id: string, body: TaskQueueInput): Promise<TaskQueue> {
  return parseAnswer(taskQueueSchema, await unwrap(client.PUT("/task-queues/{id}", { params: { path: { id } }, body })));
}

export async function deleteTaskQueue(client: ApiClient, id: string): Promise<void> {
  await unwrap(client.DELETE("/task-queues/{id}", { params: { path: { id } } }));
}

export async function fetchTaskSettings(client: ApiClient): Promise<TaskSettingsView> {
  return parseAnswer(taskSettingsViewSchema, await unwrap(client.GET("/task-settings")));
}

export async function updateTaskSettings(client: ApiClient, body: TaskSettings): Promise<TaskSettingsView> {
  return parseAnswer(taskSettingsViewSchema, await unwrap(client.PUT("/task-settings", { body })));
}

/** Tasks of a conversation, the section's settings for it and the reply about a done task. */
export async function fetchConversationTasks(client: ApiClient, conversationId: string): Promise<ConversationTasks> {
  const data = await unwrap(client.GET("/conversations/{id}/tasks", { params: { path: { id: conversationId } } }));
  return parseAnswer(conversationTasksSchema, data);
}

/** The task form filled from the conversation; the model may write it (a paid call). */
export async function fetchTaskDraft(client: ApiClient, conversationId: string): Promise<TaskDraft> {
  const data = await unwrap(client.GET("/conversations/{id}/tasks/draft", { params: { path: { id: conversationId } } }));
  return parseAnswer(taskDraftSchema, data);
}

/** The workspace's Telegram bot for notifications (TK-02); the token is never returned. */
export async function fetchTaskTelegramBot(client: ApiClient): Promise<TelegramBotState> {
  return parseAnswer(telegramBotStateSchema, await unwrap(client.GET("/task-settings/telegram-bot")));
}

export async function setTaskTelegramBot(client: ApiClient, token: string): Promise<TelegramBotState> {
  return parseAnswer(telegramBotStateSchema, await unwrap(client.PUT("/task-settings/telegram-bot", { body: { token } })));
}

export async function deleteTaskTelegramBot(client: ApiClient): Promise<void> {
  await unwrap(client.DELETE("/task-settings/telegram-bot"));
}

/** The caller's link to the notifications bot. */
export async function fetchMyTelegram(client: ApiClient): Promise<MyTelegram> {
  return parseAnswer(myTelegramSchema, await unwrap(client.GET("/me/telegram")));
}

export async function startMyTelegramLink(client: ApiClient): Promise<TelegramLinkStart> {
  return parseAnswer(telegramLinkStartSchema, await unwrap(client.POST("/me/telegram/link")), 201);
}

/** Reads the bot's new messages: links whoever pressed Start with their code. */
export async function checkMyTelegramLink(client: ApiClient): Promise<MyTelegram> {
  return parseAnswer(myTelegramSchema, await unwrap(client.POST("/me/telegram/check")));
}

export async function unlinkMyTelegram(client: ApiClient): Promise<void> {
  await unwrap(client.DELETE("/me/telegram"));
}

export type TaskFile = Schemas["TaskFile"];

/**
 * Registers a file and uploads it with the signed request, exactly as signed. The file belongs to
 * no task until `file_ids` of a task, a comment or `addTaskFiles` names it.
 */
export async function uploadTaskFile(client: ApiClient, file: File): Promise<TaskFile> {
  const body = { file_name: file.name || "file", content_type: file.type, size: file.size };
  const up = parseAnswer(taskFileUploadSchema, await unwrap(client.POST("/task-files", { body })), 201);
  // The signed request carries no cookie: the signature is the authority.
  const content = await file.arrayBuffer(); // at most 25 MB
  const res = await fetch(new Request(up.upload.url, { method: up.upload.method, headers: up.upload.headers, body: content }));
  if (!res.ok) throw new Error(`upload failed: ${res.status}`);
  return up.file;
}

export async function addTaskFiles(client: ApiClient, id: string, fileIds: string[]): Promise<Task> {
  const data = await unwrap(client.POST("/tasks/{id}/files", { params: { path: { id } }, body: { file_ids: fileIds } }));
  return parseAnswer(taskSchema, data);
}

export async function removeTaskFile(client: ApiClient, id: string, fileId: string): Promise<void> {
  await unwrap(client.DELETE("/tasks/{id}/files/{file_id}", { params: { path: { id, file_id: fileId } } }));
}

export async function fetchTaskFileLink(client: ApiClient, id: string) {
  return parseAnswer(signedRequestSchema, await unwrap(client.GET("/task-files/{id}/download-link", { params: { path: { id } } })));
}

