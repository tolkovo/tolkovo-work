# @tolkovo/widget

A live chat with your support team, for a website or an Electron app. Messages land in the Tolkovo inbox; operators answer from the panel. Everything the widget needs ships in the package (Preact included, about 16 kB gzip): it loads nothing from a CDN at runtime.

> Not published to npm yet. Until it is, build it from this repository (`pnpm --filter @tolkovo/widget build`) and add `dist/` to your app.

## 1. Connect a widget in the panel

**Settings → Channels → Connect a channel → Widget.**

1. Give it a name and list the pages that may embed it, one per line:
   - `https://shop.example.com`: one site;
   - `https://*.example.com`: every subdomain, not the apex;
   - `app://zerno`: an Electron app's own scheme (see below);
   - `file://` or `null`: an Electron app loaded from files. Any page in a sandboxed iframe also sends `null`, so a custom scheme is safer.
2. Copy the **widget key** (`tk_widget_…`). It is public and goes into the app.
3. Copy the **identity secret** (`tk_identity_…`). It is shown once and is for your **backend only**, never for the app.

## 2. Add it to the app

```ts
import { Tolkovo } from "@tolkovo/widget";

Tolkovo.init({
  widgetKey: "tk_widget_…",
  apiBase: "https://support.example.com", // where Tolkovo runs
  locale: "auto", // "ru" | "en" | "auto"
  title: "Поддержка Зерна",
  subtitle: { ru: "Обычно отвечаем за 5 минут", en: "We usually reply in 5 minutes" },
  greeting: { ru: "Здравствуйте! Чем помочь?", en: "Hi! How can we help?" },
  placeholder: { ru: "Опишите вопрос…", en: "Describe your question…" },
  logo: "data:image/svg+xml;base64,…", // an https: URL or an inline image
  theme: {
    accent: "#2f6fed", // any CSS color; the text on it is picked for contrast
    accentText: "#ffffff", // optional
    mode: "auto", // "light" | "dark" | "auto"
    radius: 12,
    position: "bottom-right", // or "bottom-left"
    offsetX: 20,
    offsetY: 20,
    zIndex: 2147483000,
    fontFamily: "Inter, system-ui, sans-serif",
  },
  launcher: true, // false: no round button, open the chat from your own menu
});
```

All looks are set here, in code. The panel holds only the key, the pages and the secret. A value the browser does not accept, such as a misspelled color, is ignored, and the default stays.

### JavaScript API

```ts
Tolkovo.open(); Tolkovo.close(); Tolkovo.toggle();
Tolkovo.show(); Tolkovo.hide(); // the launcher button
await Tolkovo.identify({ id, hash, name, email }); // the user signed in
await Tolkovo.logout(); // the user signed out: the next person starts afresh
Tolkovo.on("message", (m) => {}); // also: ready, open, close, unread, chatStarted
Tolkovo.off("message", handler);
Tolkovo.destroy();
```

## 3. Signed-in users (recommended)

When the app knows who is signed in, pass the user with a signature computed **on your backend**:

```js
// Node.js, on your server — never in the app
import crypto from "node:crypto";
const hash = crypto.createHmac("sha256", process.env.TOLKOVO_IDENTITY_SECRET).update(user.id).digest("hex");
```

```ts
await Tolkovo.identify({ id: user.id, hash, name: user.name, email: user.email });
```

With a valid `hash`, the operator sees "identity confirmed". The chat continues with the same customer card on every device, and that card also holds the user's problem reports from the app (the same `external_id`). Without a valid `hash`, the visitor is a new, unconfirmed customer and never sees anyone else's history.

Call `Tolkovo.logout()` when the user signs out.

## Electron

The widget runs in the renderer like on any page. Three things are specific to Electron.

**Origin.** The server answers only pages whose `Origin` is on the widget's list. Serve the renderer from a privileged custom scheme and add it to the list (`app://zerno`):

```js
// main process, before app is ready
const { protocol } = require("electron");
protocol.registerSchemesAsPrivileged([
  { scheme: "app", privileges: { standard: true, secure: true, supportFetchAPI: true, corsEnabled: true } },
]);
```

An app that loads `file://` pages can list `file://` and `null` instead.

**Content Security Policy.** Allow the server for HTTP and WebSocket:

```
connect-src 'self' https://support.example.com wss://support.example.com
```

The widget's styles live in a shadow root (`adoptedStyleSheets`), so no `'unsafe-inline'` is needed for them.

**Secrets.** The identity secret stays on your backend: the app asks your backend for the user's `hash` after sign-in. Never put the secret in the app.

## Script tag (sites without a bundler)

`dist/tolkovo-widget.iife.js` defines `window.Tolkovo` with the same API:

```html
<script src="/assets/tolkovo-widget.iife.js"></script>
<script>
  Tolkovo.init({ widgetKey: "tk_widget_…", apiBase: "https://support.example.com" });
</script>
```

## How it works

- **Session.** The session token is kept in `localStorage` under `tolkovo:<widgetKey>:session`, or in memory if storage is blocked. You can pass your own `storage`. A session lives 30 days after the last activity.
- **Live updates.** A WebSocket to `/api/v1/widget/realtime` carries only signals: "a reply is here", "an operator is typing". Texts always come over HTTPS. After a reconnect the widget refetches what it missed.
- **Contacts.** An anonymous visitor appears in the inbox only after their first message.
- **Receipts.** The operator sees "delivered" when the widget has a reply and "read" when it was shown in an open window.

License: [MIT](LICENSE).
