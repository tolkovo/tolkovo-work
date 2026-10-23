import { createMemoryHistory } from "@tanstack/react-router";
import { render } from "@testing-library/react";
import type { Language } from "@tolkovo/i18n";

import { App } from "../app";
import { createPanelApiClient } from "../lib/api";
import { createI18n } from "./i18n";
import { createQueryClient } from "../lib/query-client";
import { createAppRouter } from "../router";
import { fakeApi } from "./fake-api";

type RenderOptions = {
  language?: Language;
  /** The fake API; by default the owner is signed in. */
  api?: ReturnType<typeof fakeApi>;
};

/** Renders the real app (providers, route tree, shell) at a URL, against a fake API. */
export function renderApp(url: string, { language = "ru", api = fakeApi() }: RenderOptions = {}) {
  const i18n = createI18n(language);
  const queryClient = createQueryClient();
  const client = createPanelApiClient(queryClient, { origin: "http://panel.test", fetch: api.fetch });
  const router = createAppRouter({
    queryClient,
    api: client,
    history: createMemoryHistory({ initialEntries: [url] }),
  });
  const result = render(<App router={router} queryClient={queryClient} api={client} i18n={i18n} />);
  return { ...result, router, i18n, queryClient, api };
}
