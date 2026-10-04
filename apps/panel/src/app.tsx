import { QueryClientProvider, type QueryClient } from "@tanstack/react-query";
import { RouterProvider } from "@tanstack/react-router";
import type { i18n } from "i18next";
import { I18nextProvider } from "react-i18next";

import type { createAppRouter } from "./router";

type AppProps = {
  router: ReturnType<typeof createAppRouter>;
  queryClient: QueryClient;
  i18n: i18n;
};

/** Providers in one place, so tests render exactly what users get. */
export function App({ router, queryClient, i18n }: AppProps) {
  return (
    <I18nextProvider i18n={i18n}>
      <QueryClientProvider client={queryClient}>
        <RouterProvider router={router} />
      </QueryClientProvider>
    </I18nextProvider>
  );
}
