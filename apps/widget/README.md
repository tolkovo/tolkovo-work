# @tolkovo/widget

A live chat with your support team, for a website or an Electron app. Messages land in the Tolkovo inbox; operators answer from the panel. Everything the widget needs ships in the package (Preact included, about 25 kB gzip): it loads nothing from a CDN at runtime.

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

All looks are set here, in code. The panel holds only the key, the pages, the secret and where goals go (below). A value the browser does not accept, such as a misspelled color, is ignored, and the default stays.

### JavaScript API

```ts
Tolkovo.open(); Tolkovo.close(); Tolkovo.toggle();
Tolkovo.show(); Tolkovo.hide(); // the launcher button
await Tolkovo.identify({ id, hash, name, email }); // the user signed in
await Tolkovo.logout(); // the user signed out: the next person starts afresh
Tolkovo.on("message", (m) => {}); // also: ready, open, close, unread, chatStarted, contactCollected, rated
Tolkovo.off("message", handler);
Tolkovo.destroy();
```

### Pre-chat form

Ask who the visitor is before the first message:

```ts
Tolkovo.init({
  // …
  prechat: {
    fields: ["name", "email", "phone"], // in this order; default ["name", "email"]
    required: ["name"], // default: none
    intro: { ru: "Представьтесь, чтобы мы ответили на почту", en: "Tell us where to reply" }, // optional
    consent: { policyUrl: "https://example.com/privacy" }, // a required checkbox with a link to the policy
  },
});
```

The form replaces the message field until the visitor sends it. A visitor you identified (`user` or `identify`) is not asked. Neither is a visitor who has already filled the form in for this session. What they type fills the customer card. The name replaces the guest name ("Гость 4821"), but values an operator entered are never overwritten. The customer shows as "identity not confirmed", because the values are only what the visitor typed. The consent is stored with the policy link and the time. After the form is sent, the `contactCollected` event fires with `{ source: "prechat" }` only, never the values.

### Offline form

When nobody on your team takes conversations (no one has the panel open with the status "Online"), a visitor who has not written yet sees a form instead of the chat. It asks for a message and an email or a phone. Once the visitor sends it, the message goes to the queue as their first one, with the contact on the customer card. The chat then says the team will reply once it is back. A visitor already in a conversation keeps chatting and sees the same line. The form is on by default:

```ts
Tolkovo.init({
  // …
  offline: {
    fields: ["name", "phone"], // default ["name", "email"]; the email or the phone is always asked
    intro: { ru: "Мы на связи с 9 до 18. Оставьте сообщение", en: "We are here 9 to 6. Leave a message" }, // optional
    consent: { policyUrl: "https://example.com/privacy" }, // default: the pre-chat form's consent
  },
  // offline: false — keep the chat open even when nobody is there
});
```

The pre-chat form is not shown on top of it. After the form is sent, `contactCollected` fires with `{ source: "offline" }`.

### Rating

When an operator resolves the conversation, the chat asks the visitor to rate it: five stars and an optional comment. The operator sees the rating in the conversation. Rating again replaces it; a new message reopens the conversation and the request goes away. After a rating, `rated` fires with `{ score, resolvedBy }`.

### Goals in Yandex Metrica and Google Analytics

The widget can report three goals to the counters your site already has: a visitor started a chat (`chatStarted`), left a contact (`contactCollected`), rated the conversation (`rated`). Turn them on in the panel, not in code: **Settings → Channels → your widget → Site analytics**.

- **Google Tag Manager, or GA4 through GTM**: `window.dataLayer.push({ event: "tolkovo.chatStarted", tolkovo: { channel: "widget", page: "/checkout" } })`.
- **GA4 without GTM**: `gtag("event", "tolkovo.chatStarted", { ... })`.
- **Yandex Metrica**: `ym(<counter>, "reachGoal", "tolkovo.chatStarted", { ... })`.

Goal names can be changed in the same place. Parameters never carry texts, names, contacts or ids: `chatStarted` has `channel` and `page` (the path without the query; nothing for a page loaded from a file), `contactCollected` has `source`, `rated` has `score` and `resolvedBy`. The widget loads no counter and sets no cookie: a counter the page does not have is skipped, so your cookie consent works as before. The same events reach `Tolkovo.on(...)`.

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

**Content Security Policy.** Allow the server for HTTP and WebSocket, and for the pictures in the chat:

```
connect-src 'self' https://support.example.com wss://support.example.com
img-src 'self' https://support.example.com
```

With the `s3` storage driver, files are uploaded to the bucket and shown from it: add the bucket's address to `connect-src` and `img-src` too. The same applies to a site with a CSP.

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
- **Contacts.** An anonymous visitor appears in the inbox only after their first message, as "Гость 4821" ("Guest 4821" in an English workspace) until they or an operator give a name.
- **Receipts.** The operator sees "delivered" when the widget has a reply and "read" when it was shown in an open window.
- **Files.** The visitor attaches up to 5 files per message with the paperclip button, by pasting a screenshot or by dragging files onto the window. Each file can be up to 10 MB: a picture, PDF, text, CSV, JSON, ZIP, DOCX or XLSX. Before the message is stored, the server checks that a file is what its type says and, if your server runs an antivirus, scans it. A file that fails is deleted, and the visitor sees which one did not go. Pictures in the chat show as previews. Other files open from a link.

License: [MIT](LICENSE).
