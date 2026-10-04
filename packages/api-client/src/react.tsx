import {
  queryOptions,
  useMutation,
  useQuery,
  useQueryClient,
  type QueryClient,
} from "@tanstack/react-query";
import { createContext, useContext, type ReactNode } from "react";

import type { ApiClient } from "./client";
import {
  fetchMembers,
  fetchMyWorkspaces,
  fetchSession,
  hasPermission,
  login,
  logout,
  register,
  switchWorkspace,
  updateWorkspace,
  type LoginRequest,
  type RegisterRequest,
  type Session,
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
