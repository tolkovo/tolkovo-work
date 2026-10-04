import { createMemoryHistory } from "@tanstack/react-router";
import { render } from "@testing-library/react";
import type { Language } from "@tolkovo/i18n";

import { App } from "../app";
import { createI18n } from "../lib/i18n";
import { createQueryClient } from "../lib/query-client";
import { createAppRouter } from "../router";

/** Renders the real app (providers, route tree, shell) at a URL. */
export function renderApp(url: string, { language = "ru" }: { language?: Language } = {}) {
  const i18n = createI18n(language);
  const queryClient = createQueryClient();
  const router = createAppRouter({ queryClient, history: createMemoryHistory({ initialEntries: [url] }) });
  const result = render(<App router={router} queryClient={queryClient} i18n={i18n} />);
  return { ...result, router, i18n };
}
