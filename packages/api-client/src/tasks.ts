import { parseAnswer, unwrap, type ApiClient } from "./client";
import type { components } from "./schema.gen";
import {
  conversationTasksSchema,
  taskActivityListSchema,
  taskActivitySchema,
  taskBoardSchema,
  taskBoardSettingsSchema,
  taskDraftSchema,
  taskPageSchema,
  taskQueueListSchema,
  taskQueueSchema,
  signedRequestSchema,
  taskFileUploadSchema,
  taskSchema,
  taskSettingsViewSchema,
  taskSummarySchema,
  tagListSchema,
  tagSchema,
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
export type TaskBoard = Schemas["TaskBoard"];
export type TaskBoardColumn = Schemas["TaskBoardColumn"];
export type TaskBoardSettings = Schemas["TaskBoardSettings"];
export type TaskColumnRule = Schemas["TaskColumnRule"];
export type TaskCreate = Schemas["TaskCreate"];
export type TaskUpdate = Schemas["TaskUpdate"];
export type TaskActivity = Schemas["TaskActivity"];
export type TaskQueue = Schemas["TaskQueue"];
export type TaskQueueInput = Schemas["TaskQueueInput"];
/** A tag of tasks has the shape of a conversation's tag, in a list of its own (GET /task-tags). */
export type TaskTag = Schemas["Tag"];
export type TaskTagInput = Schemas["TaskTagInput"];
export type TaskSettings = Schemas["TaskSettings"];
export type TaskSettingsView = Schemas["TaskSettingsView"];
export type ConversationTasks = Schemas["ConversationTasks"];
export type TaskDraft = Schemas["TaskDraft"];
export type MyTelegram = Schemas["MyTelegram"];
export type TelegramLinkStart = Schemas["TelegramLinkStart"];
export type TelegramBotState = Schemas["TelegramBotState"];

/**
 * Filter of GET /tasks; `assignee` is `me`, `none` or a member id, `queue` and `topic` are
 * `none` or an id, `tag` is a tag id.
 */
export type TaskFilter = {
  state?: "open" | "closed";
  status?: TaskStatus;
  assignee?: string;
  queue?: string;
  topic?: string;
  tag?: string;
  blocked?: boolean;
  q?: string;
};

export async function fetchTasks(client: ApiClient, filter: TaskFilter, cursor?: string): Promise<TaskPage> {
  const query = { ...filter, ...(cursor ? { cursor } : {}) };
  return parseAnswer(taskPageSchema, await unwrap(client.GET("/tasks", { params: { query } })));
}

/** Filter of the board (ADR-0019): the list's, without the state; `closed_days` bounds done. */
export type TaskBoardFilter = Omit<TaskFilter, "state" | "status"> & { closed_days?: number; canceled?: boolean };

/** The whole board in one answer, a column by status, so the counts agree. */
export async function fetchTaskBoard(client: ApiClient, filter: TaskBoardFilter): Promise<TaskBoard> {
  return parseAnswer(taskBoardSchema, await unwrap(client.GET("/tasks/board", { params: { query: filter } })));
}

/** Soft limits and age thresholds of the board's open columns, the workspace's and the queues'. */
export async function fetchTaskBoardSettings(client: ApiClient): Promise<TaskBoardSettings> {
  return parseAnswer(taskBoardSettingsSchema, await unwrap(client.GET("/task-board-settings")));
}

export async function updateTaskBoardSettings(client: ApiClient, body: TaskBoardSettings): Promise<TaskBoardSettings> {
  return parseAnswer(taskBoardSettingsSchema, await unwrap(client.PUT("/task-board-settings", { body })));
}

/** The rest of a board column from its `next_cursor`, in the order of the column. */
export async function fetchTaskColumn(client: ApiClient, filter: TaskBoardFilter, status: TaskStatus, cursor: string): Promise<TaskPage> {
  const { canceled: _, ...rest } = filter;
  const query = { ...rest, status, sort: "flow" as const, cursor };
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

export async function fetchTaskTags(client: ApiClient): Promise<TaskTag[]> {
  return parseAnswer(tagListSchema, await unwrap(client.GET("/task-tags"))).items;
}

/** Any member adds a tag of tasks; a taken name answers `409 tag_exists`. */
export async function createTaskTag(client: ApiClient, body: TaskTagInput): Promise<TaskTag> {
  return parseAnswer(tagSchema, await unwrap(client.POST("/task-tags", { body })), 201);
}

export async function updateTaskTag(client: ApiClient, id: string, body: TaskTagInput): Promise<TaskTag> {
  return parseAnswer(tagSchema, await unwrap(client.PUT("/task-tags/{id}", { params: { path: { id } }, body })));
}

export async function deleteTaskTag(client: ApiClient, id: string): Promise<void> {
  await unwrap(client.DELETE("/task-tags/{id}", { params: { path: { id } } }));
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

