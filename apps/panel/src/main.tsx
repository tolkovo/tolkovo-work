import "./fonts.css";
import "./app.css";

import { StrictMode } from "react";
import { createRoot } from "react-dom/client";

import { App } from "./app";
import { createPanelApiClient } from "./lib/api";
import { createI18n } from "./lib/i18n";
import { installNotifier } from "./lib/notifications/instance";
import { createLeader } from "./lib/notifications/leader";
import { createNotifier } from "./lib/notifications/notifier";
import { createSoundPlayer } from "./lib/notifications/sound";
import { applyTheme, readStoredTheme } from "./lib/preferences";
import { createQueryClient } from "./lib/query-client";
import { realtimeUrl } from "./lib/realtime/protocol";
import { bindRealtime } from "./lib/realtime/session-binding";
import { createAppRouter } from "./router";

const rootElement = document.getElementById("root");
if (!rootElement) throw new Error("#root element is missing in index.html");

applyTheme(readStoredTheme());
const i18n = createI18n();
const queryClient = createQueryClient();
const api = createPanelApiClient(queryClient);
const router = createAppRouter({ queryClient, api });
// Sounds and desktop notifications (I-05); the browser lets audio start only after a gesture.
const sound = createSoundPlayer();
for (const type of ["pointerdown", "keydown"] as const) window.addEventListener(type, sound.unlock, { once: true });
const notifier = createNotifier({
  sound,
  leader: createLeader(),
  t: i18n.t.bind(i18n),
  openConversationId: () => /^\/inbox\/([^/]+)/.exec(router.state.location.pathname)?.[1],
  openConversation: (id) =>
    void router.navigate({ to: "/inbox/$conversationId", params: { conversationId: id }, search: { tab: "mine" } }),
});
installNotifier(notifier);
// Live updates for the signed-in session; lives as long as the page.
bindRealtime(queryClient, { url: realtimeUrl(window.location.origin), api, notifier });

createRoot(rootElement).render(
  <StrictMode>
    <App router={router} queryClient={queryClient} api={api} i18n={i18n} />
  </StrictMode>,
);
