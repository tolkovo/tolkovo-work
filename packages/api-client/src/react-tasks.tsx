import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import type { ApiClient } from "./client";
import {
  addTaskFiles,
  commentTask,
  fetchTaskFileLink,
  removeTaskFile,
  createTask,
  createTaskQueue,
  createTaskTag,
  deleteTask,
  deleteTaskQueue,
  deleteTaskTag,
  fetchConversationTasks,
  fetchTask,
  fetchTaskActivity,
  fetchTaskBoard,
  fetchTaskBoardSettings,
  fetchTaskColumn,
  fetchTaskDraft,
  fetchTaskQueues,
  fetchTaskTags,
  fetchTasks,
  fetchTaskSettings,
  fetchTaskSummary,
  linkTaskConversation,
  unlinkTaskConversation,
  updateTask,
  updateTaskBoardSettings,
  updateTaskQueue,
  updateTaskSettings,
  updateTaskTag,
  fetchTaskTelegramBot,
  setTaskTelegramBot,
  deleteTaskTelegramBot,
  fetchMyTelegram,
  startMyTelegramLink,
  checkMyTelegramLink,
  unlinkMyTelegram,
  type Task,
  type TaskBoard,
  type TaskBoardFilter,
  type TaskBoardSettings,
  type TaskCreate,
  type TaskFilter,
  type TaskStatus,
  type TaskQueueInput,
  type TaskSettings,
  type TaskTagInput,
  type TaskUpdate,
} from "./tasks";
import { queryKeys, useApiClient } from "./react-core";

/** Hooks of the tasks section (ADR-0015) and of the Telegram notifications about tasks (TK-02). */

/** Pages of tasks for one filter, newest first (ADR-0015). */
export function useTasks(filter: TaskFilter, { enabled = true }: { enabled?: boolean } = {}) {
  const client = useApiClient();
  return useInfiniteQuery({
    queryKey: queryKeys.taskList(filter),
    queryFn: ({ pageParam }) => fetchTasks(client, filter, pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next_cursor ?? undefined,
    enabled,
  });
}

/** The board of tasks (ADR-0019): every column in one answer. */
export function useTaskBoard(filter: TaskBoardFilter, { enabled = true }: { enabled?: boolean } = {}) {
  const client = useApiClient();
  return useQuery({ queryKey: queryKeys.taskBoard(filter), queryFn: () => fetchTaskBoard(client, filter), enabled });
}

export function useTaskBoardSettings({ enabled = true }: { enabled?: boolean } = {}) {
  const client = useApiClient();
  return useQuery({ queryKey: queryKeys.taskBoardSettings, queryFn: () => fetchTaskBoardSettings(client), enabled });
}

/** Replaces the rules; the board reads them again with its columns. */
export function useUpdateTaskBoardSettings() {
  return useTaskMutation((client, body: TaskBoardSettings) => updateTaskBoardSettings(client, body));
}

/** The rest of one board column, loaded on demand from the column's `next_cursor`. */
export function useTaskColumnMore(filter: TaskBoardFilter) {
  const client = useApiClient();
  return useMutation({
    mutationFn: ({ status, cursor }: { status: TaskStatus; cursor: string }) => fetchTaskColumn(client, filter, status, cursor),
  });
}

/**
 * Moves a task on the board by PUT /tasks/{id}: `place` moves the card at once, and a failure
 * (`409 task_conflict` among them) puts the board back before the section refreshes.
 */
export function useMoveTask(filter: TaskBoardFilter, place: (board: TaskBoard, task: Task, body: TaskUpdate) => TaskBoard) {
  const client = useApiClient();
  const queryClient = useQueryClient();
  const key = queryKeys.taskBoard(filter);
  return useMutation({
    mutationFn: ({ task, body }: { task: Task; body: TaskUpdate }) => updateTask(client, task.id, body),
    onMutate: async ({ task, body }) => {
      await queryClient.cancelQueries({ queryKey: key });
      const before = queryClient.getQueryData<TaskBoard>(key);
      if (before) queryClient.setQueryData(key, place(before, task, body));
      return { before };
    },
    onError: (_err, _args, ctx) => {
      if (ctx?.before) queryClient.setQueryData(key, ctx.before);
    },
    // Not awaited: the caller hears of the move at once, the board refreshes behind it.
    onSettled: () => {
      void queryClient.invalidateQueries({ queryKey: queryKeys.tasks });
      void queryClient.invalidateQueries({ queryKey: queryKeys.conversations });
    },
  });
}

export function useTask(id: string) {
  const client = useApiClient();
  return useQuery({ queryKey: queryKeys.task(id), queryFn: () => fetchTask(client, id) });
}

export function useTaskActivity(id: string) {
  const client = useApiClient();
  return useQuery({ queryKey: queryKeys.taskActivity(id), queryFn: () => fetchTaskActivity(client, id) });
}

/** Open tasks assigned to the caller, for the counter in the rail. */
export function useTaskSummary({ enabled = true }: { enabled?: boolean } = {}) {
  const client = useApiClient();
  return useQuery({ queryKey: queryKeys.taskSummary, queryFn: () => fetchTaskSummary(client), enabled });
}

export function useTaskQueues({ enabled = true }: { enabled?: boolean } = {}) {
  const client = useApiClient();
  return useQuery({ queryKey: queryKeys.taskQueues, queryFn: () => fetchTaskQueues(client), enabled });
}

/** Tags of tasks, a list apart from the tags of conversations (useTags). */
export function useTaskTags({ enabled = true }: { enabled?: boolean } = {}) {
  const client = useApiClient();
  return useQuery({ queryKey: queryKeys.taskTags, queryFn: () => fetchTaskTags(client), enabled, staleTime: 5 * 60_000 });
}

export function useTaskSettings({ enabled = true }: { enabled?: boolean } = {}) {
  const client = useApiClient();
  return useQuery({ queryKey: queryKeys.taskSettings, queryFn: () => fetchTaskSettings(client), enabled });
}

/** Tasks of a conversation; conversation.tasks_changed refreshes it over the realtime connection. */
export function useConversationTasks(conversationId: string, { enabled = true }: { enabled?: boolean } = {}) {
  const client = useApiClient();
  return useQuery({
    queryKey: queryKeys.conversationTasks(conversationId),
    queryFn: () => fetchConversationTasks(client, conversationId),
    enabled,
  });
}

/** The task form drafted from a conversation; fetched anew every time the form opens. */
export function useTaskDraft(conversationId: string, { enabled = true }: { enabled?: boolean } = {}) {
  const client = useApiClient();
  return useQuery({
    queryKey: queryKeys.taskDraft(conversationId),
    queryFn: () => fetchTaskDraft(client, conversationId),
    enabled,
    gcTime: 0,
    // The model may write the draft: every fetch is paid for, and the form keeps its own edits.
    staleTime: Infinity,
    refetchOnWindowFocus: false,
    retry: false,
  });
}

/**
 * A task change refreshes the section and, when it touches conversations, their tasks, the
 * conversations themselves (awaiting_fix, status) and their feeds.
 */
function useTaskMutation<TArgs, TResult>(fn: (client: ApiClient, args: TArgs) => Promise<TResult>) {
  const client = useApiClient();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (args: TArgs) => fn(client, args),
    onSettled: () =>
      Promise.all([
        queryClient.invalidateQueries({ queryKey: queryKeys.tasks }),
        queryClient.invalidateQueries({ queryKey: queryKeys.conversations }),
      ]),
  });
}

export function useCreateTask() {
  return useTaskMutation((client, body: TaskCreate) => createTask(client, body));
}

export function useUpdateTask(id: string) {
  return useTaskMutation((client, body: TaskUpdate) => updateTask(client, id, body));
}

export function useDeleteTask() {
  return useTaskMutation((client, id: string) => deleteTask(client, id));
}

export function useCommentTask(id: string) {
  return useTaskMutation((client, { body, fileIds = [] }: { body: string; fileIds?: string[] }) => commentTask(client, id, body, fileIds));
}

export function useAddTaskFiles(id: string) {
  return useTaskMutation((client, fileIds: string[]) => addTaskFiles(client, id, fileIds));
}

export function useRemoveTaskFile(id: string) {
  return useTaskMutation((client, fileId: string) => removeTaskFile(client, id, fileId));
}

/** The signed link of a file of a task, for showing a picture; links live 15 minutes. */
export function useTaskFileLink(id: string, { enabled = true }: { enabled?: boolean } = {}) {
  const client = useApiClient();
  return useQuery({
    // Outside "tasks": changing a task must not sign every link again.
    queryKey: ["task-file-link", id] as const,
    queryFn: () => fetchTaskFileLink(client, id),
    enabled,
    staleTime: 10 * 60_000,
    gcTime: 10 * 60_000,
    refetchOnWindowFocus: false,
  });
}

export function useLinkTaskConversation() {
  return useTaskMutation((client, { taskId, conversationId }: { taskId: string; conversationId: string }) =>
    linkTaskConversation(client, taskId, conversationId),
  );
}

export function useUnlinkTaskConversation() {
  return useTaskMutation((client, { taskId, conversationId }: { taskId: string; conversationId: string }) =>
    unlinkTaskConversation(client, taskId, conversationId),
  );
}

export function useCreateTaskQueue() {
  return useTaskMutation((client, body: TaskQueueInput) => createTaskQueue(client, body));
}

export function useUpdateTaskQueue() {
  return useTaskMutation((client, { id, ...body }: TaskQueueInput & { id: string }) => updateTaskQueue(client, id, body));
}

export function useDeleteTaskQueue() {
  return useTaskMutation((client, id: string) => deleteTaskQueue(client, id));
}

export function useCreateTaskTag() {
  return useTaskMutation((client, body: TaskTagInput) => createTaskTag(client, body));
}

export function useUpdateTaskTag() {
  return useTaskMutation((client, { id, ...body }: TaskTagInput & { id: string }) => updateTaskTag(client, id, body));
}

export function useDeleteTaskTag() {
  return useTaskMutation((client, id: string) => deleteTaskTag(client, id));
}

export function useUpdateTaskSettings() {
  return useTaskMutation((client, body: TaskSettings) => updateTaskSettings(client, body));
}

/** The workspace's notifications bot (TK-02). */
export function useTaskTelegramBot({ enabled = true }: { enabled?: boolean } = {}) {
  const client = useApiClient();
  return useQuery({ queryKey: queryKeys.taskTelegramBot, queryFn: () => fetchTaskTelegramBot(client), enabled });
}

function useBotMutation<TArgs, TResult>(fn: (client: ApiClient, args: TArgs) => Promise<TResult>) {
  const client = useApiClient();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (args: TArgs) => fn(client, args),
    onSettled: () =>
      Promise.all([
        queryClient.invalidateQueries({ queryKey: queryKeys.taskTelegramBot }),
        queryClient.invalidateQueries({ queryKey: queryKeys.myTelegram }),
      ]),
  });
}

export function useSetTaskTelegramBot() {
  return useBotMutation((client, token: string) => setTaskTelegramBot(client, token));
}

export function useDeleteTaskTelegramBot() {
  return useBotMutation((client, _: void) => deleteTaskTelegramBot(client));
}

/**
 * The caller's Telegram link. While a link waits for Start the server is asked every three
 * seconds to read the bot's messages, until the link is done or its code expires.
 */
export function useMyTelegram() {
  const client = useApiClient();
  return useQuery({
    queryKey: queryKeys.myTelegram,
    queryFn: () => fetchMyTelegram(client),
  });
}

export function useCheckMyTelegram({ enabled }: { enabled: boolean }) {
  const client = useApiClient();
  const queryClient = useQueryClient();
  return useQuery({
    queryKey: [...queryKeys.myTelegram, "check"],
    queryFn: async () => {
      const me = await checkMyTelegramLink(client);
      queryClient.setQueryData(queryKeys.myTelegram, me);
      return me;
    },
    enabled,
    refetchInterval: (q) => (q.state.data?.linked ? false : 3000),
    retry: false,
  });
}

export function useStartMyTelegramLink() {
  return useBotMutation((client, _: void) => startMyTelegramLink(client));
}

export function useUnlinkMyTelegram() {
  return useBotMutation((client, _: void) => unlinkMyTelegram(client));
}

/** The knowledge base of the AI bot (ADR-0018). */
