import type { ApiClient } from "@tolkovo/api-client";
import type { QueryClient } from "@tanstack/react-query";
import { createRouter, type RouterHistory } from "@tanstack/react-router";

import { ErrorView } from "./components/error-view";
import { NotFound } from "./components/not-found";
import { PageSkeleton } from "./components/page-skeleton";
import { routeTree } from "./routeTree.gen";

export type RouterContext = {
  queryClient: QueryClient;
  api: ApiClient;
};

export function createAppRouter({ queryClient, api, history }: RouterContext & { history?: RouterHistory }) {
  return createRouter({
    routeTree,
    context: { queryClient, api },
    ...(history ? { history } : {}),
    defaultPreload: "intent",
    // Loader data lives in TanStack Query; the router must not cache it a second time.
    defaultPreloadStaleTime: 0,
    defaultPendingComponent: PageSkeleton,
    defaultErrorComponent: ErrorView,
    defaultNotFoundComponent: NotFound,
    scrollRestoration: true,
  });
}

declare module "@tanstack/react-router" {
  interface Register {
    router: ReturnType<typeof createAppRouter>;
  }
}
