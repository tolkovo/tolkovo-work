import "./app.css";

import { StrictMode } from "react";
import { createRoot } from "react-dom/client";

import { App } from "./app";
import { createI18n } from "./lib/i18n";
import { applyTheme, readStoredTheme } from "./lib/preferences";
import { createQueryClient } from "./lib/query-client";
import { createAppRouter } from "./router";

const rootElement = document.getElementById("root");
if (!rootElement) throw new Error("#root element is missing in index.html");

applyTheme(readStoredTheme());
const i18n = createI18n();
const queryClient = createQueryClient();
const router = createAppRouter({ queryClient });

createRoot(rootElement).render(
  <StrictMode>
    <App router={router} queryClient={queryClient} i18n={i18n} />
  </StrictMode>,
);
