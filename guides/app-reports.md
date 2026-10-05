# Problem reports from your app

A "Report a problem" button in a desktop or mobile app: the user describes the problem, the app attaches its logs and a screenshot, and the report lands in the Tolkovo inbox as a conversation. Operators answer from the panel; the app shows the answers and the user can write back into the same conversation.

This page is for the app's developer. The full contract is in [`api/openapi.yaml`](../api/openapi.yaml), tag `ingest`.

## 1. Get the keys

An admin opens **Settings → Channels → Connect a channel → App** and gets two values, each shown once:

| Value | Looks like | Where it goes |
|---|---|---|
| Ingest key | `tk_ingest_…` | Into the app. It can only send reports and read their replies. If it leaks, issue a new one under **Keys and limits** and revoke the old one. |
| Identity signing secret | `tk_identity_…` | Your backend only, never the app. It proves which user sent a report (§5). |

Every request below goes to `https://<your Tolkovo>/api/v1` with `Authorization: Bearer tk_ingest_…`.

## 2. How a report travels

```
app                                   Tolkovo                         storage
 │ POST /ingest/reports  ───────────►  registers the report,
 │   message, app facts, files          returns report_token
 │ ◄───────────────────────────────── and an upload link per file
 │ PUT each file  ──────────────────────────────────────────────────►  (link from the answer)
 │ POST /ingest/reports/{id}/complete ►  checks the files, queues it
 │                                      a few seconds later: a conversation in the inbox
 │ GET  /ingest/reports/{id}/replies ►  what operators answered
 │ POST /ingest/reports/{id}/messages►  the user writes back
```

1. **Create.** Send the user's text, facts about the app and a description of each file: name, size, type, SHA-256. The answer has `report_id`, `report_token` and `uploads`, one per file in the same order.
2. **Upload.** Send each file with the request in `uploads[i].upload`: its `method`, `url` and **every header as given**. The link lives 15 minutes and accepts only that file (size, type, checksum).
3. **Complete.** `POST /ingest/reports/{id}/complete` with the header `X-Report-Token`. The answer is `202`. A file that did not arrive answers `409 upload_incomplete`.
4. **Keep the report.** Store `report_id` and `report_token` on the device. The token is shown once: it is what lets this device, and only it, read the replies.
5. **Replies.** Poll `GET /ingest/reports/{id}/replies` with `X-Report-Token` when the app starts and at most once a minute while the report window is open. Pass `cursor` from the answer as `?since=` next time.
6. **Write back.** `POST /ingest/reports/{id}/messages` with `{"message": "…"}`, `X-Report-Token` and an `Idempotency-Key`.

### What a report may hold

| | Limit |
|---|---|
| Message | 1–10 000 characters |
| Files | up to 5, each up to 25 MB, up to 50 MB together (the admin can lower these) |
| Types | `text/plain`, `text/csv`, `application/json`, `application/xml`, `application/zip`, `application/gzip`, `image/png`, `image/jpeg` |
| `app` | `name`, `version`, `os`, `os_version`, `locale`, and `extra`: up to 20 more facts, 500 characters each |

A `.log` file may be declared as `text/x-log` or `application/octet-stream`: it is taken as `text/plain`, so upload it with the headers of its link. Tolkovo checks each file by its bytes: a "screenshot" that is really something else does not reach operators.

### Retries and errors

- Send an `Idempotency-Key` (any unique string, e.g. a UUID) with `POST /ingest/reports`. Repeating the request with the same key and body, before the report is completed, returns the same report with a new token and fresh links. Use it when the connection drops.
- Errors are `application/problem+json` with a stable `code`:

| Status | `code` | What to do |
|---|---|---|
| 401 | `invalid_key` | The key is wrong or revoked: ship a build with a new key |
| 404 | `not_found` | Wrong report id or token |
| 409 | `upload_incomplete` | Upload the file named in `detail`, then complete again |
| 409 | `report_processing` | The report is not a conversation yet: retry after `Retry-After` seconds |
| 409 | `report_submitted` | The `Idempotency-Key` names a completed report: use a new key for a new report |
| 413 | `too_many_files`, `file_too_large`, `report_too_large` | Drop or trim files |
| 415 | `unsupported_type` | Pack the file into a ZIP or send it as text |
| 422 | `idempotency_key_reused` | The same key came with a different body |
| 429 | | Wait `Retry-After` seconds |

Rates: 10 new reports a minute per IP and 60 an hour per key, plus the channel's daily limits; replies 2 reads a minute per report; messages 10 an hour per report.

## 3. curl

```sh
TOLKOVO=https://support.example.com/api/v1
KEY=tk_ingest_...
LOG=zerno.log

SIZE=$(wc -c < "$LOG" | tr -d ' ')
SHA=$(shasum -a 256 "$LOG" | cut -d' ' -f1)      # sha256sum on Linux

# 1. Create
curl -s "$TOLKOVO/ingest/reports" \
  -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" \
  -H "Idempotency-Key: $(uuidgen)" \
  -d "{\"message\":\"After sign-in the cart is empty\",
       \"app\":{\"name\":\"Zerno\",\"version\":\"2.4.1\",\"os\":\"macOS\",\"os_version\":\"15.1\"},
       \"files\":[{\"name\":\"$LOG\",\"size\":$SIZE,\"content_type\":\"text/plain\",\"sha256\":\"$SHA\"}]}" \
  > created.json

ID=$(jq -r .report_id created.json)
TOKEN=$(jq -r .report_token created.json)

# 2. Upload, with every header of the link
jq -r '.uploads[0].upload.headers | to_entries[] | "\(.key): \(.value)"' created.json > headers.txt
curl -s -X "$(jq -r '.uploads[0].upload.method' created.json)" "$(jq -r '.uploads[0].upload.url' created.json)" \
  -H @headers.txt --data-binary @"$LOG"

# 3. Complete
curl -s -X POST "$TOLKOVO/ingest/reports/$ID/complete" \
  -H "Authorization: Bearer $KEY" -H "X-Report-Token: $TOKEN"

# Later: replies, then write back
curl -s "$TOLKOVO/ingest/reports/$ID/replies" -H "Authorization: Bearer $KEY" -H "X-Report-Token: $TOKEN"
curl -s "$TOLKOVO/ingest/reports/$ID/messages" \
  -H "Authorization: Bearer $KEY" -H "X-Report-Token: $TOKEN" \
  -H "Content-Type: application/json" -H "Idempotency-Key: $(uuidgen)" \
  -d '{"message":"Reinstalling did not help"}'
```

## 4. Node.js and Electron

Node.js 18 or newer, no dependencies. In Electron this runs in the **main process**: the ingest API does not answer browser cross-origin requests, so the renderer asks the main process over IPC.

```js
// tolkovo-report.mjs
import { createHash, randomUUID } from "node:crypto";
import { readFile } from "node:fs/promises";
import { basename } from "node:path";

const TYPES = { ".log": "text/plain", ".txt": "text/plain", ".json": "application/json",
  ".zip": "application/zip", ".gz": "application/gzip", ".png": "image/png", ".jpg": "image/jpeg" };

export class TolkovoReports {
  constructor({ baseUrl, key }) {
    this.api = baseUrl.replace(/\/$/, "") + "/api/v1/ingest/reports";
    this.key = key;
  }

  async #call(path, { method = "GET", token, body, idempotencyKey } = {}) {
    const headers = { Authorization: `Bearer ${this.key}` };
    if (token) headers["X-Report-Token"] = token;
    if (idempotencyKey) headers["Idempotency-Key"] = idempotencyKey;
    if (body) headers["Content-Type"] = "application/json";
    const res = await fetch(this.api + path, { method, headers, body: body && JSON.stringify(body) });
    const data = res.status === 202 ? null : await res.json();
    if (!res.ok) throw Object.assign(new Error(data?.title ?? res.statusText), { status: res.status, code: data?.code });
    return data;
  }

  /** Sends a report; returns { id, token, verified } to keep on the device. */
  async send({ message, app, contact, files = [] }) {
    const loaded = await Promise.all(files.map(async (path) => {
      const bytes = await readFile(path);
      const ext = path.slice(path.lastIndexOf(".")).toLowerCase();
      return { bytes, meta: { name: basename(path), size: bytes.length,
        content_type: TYPES[ext] ?? "application/octet-stream",
        sha256: createHash("sha256").update(bytes).digest("hex") } };
    }));
    const created = await this.#call("", { method: "POST", idempotencyKey: randomUUID(),
      body: { message, app, contact, files: loaded.map((f) => f.meta) } });

    for (const [i, { upload }] of created.uploads.entries()) {
      // Send every header as given; fetch sets Content-Length from the body itself.
      const headers = Object.fromEntries(Object.entries(upload.headers)
        .filter(([name]) => name.toLowerCase() !== "content-length"));
      const res = await fetch(upload.url, { method: upload.method, headers, body: loaded[i].bytes });
      if (!res.ok) throw new Error(`upload ${loaded[i].meta.name}: ${res.status}`);
    }
    await this.#call(`/${created.report_id}/complete`, { method: "POST", token: created.report_token });
    return { id: created.report_id, token: created.report_token, verified: created.contact_verified };
  }

  /** Replies after `since`; returns { items, cursor, more }. */
  replies({ id, token }, since = "") {
    return this.#call(`/${id}/replies${since ? `?since=${encodeURIComponent(since)}` : ""}`, { token });
  }

  /** The user writes back into the conversation. */
  write({ id, token }, message) {
    return this.#call(`/${id}/messages`, { method: "POST", token, body: { message }, idempotencyKey: randomUUID() });
  }
}
```

Using it:

```js
const reports = new TolkovoReports({ baseUrl: "https://support.example.com", key: process.env.TOLKOVO_INGEST_KEY });
const report = await reports.send({
  message: "After sign-in the cart is empty",
  app: { name: "Zerno", version: "2.4.1", os: process.platform, os_version: process.getSystemVersion?.() },
  files: ["/path/to/zerno.log"],
});
// Store `report` on the device. Later:
const { items, cursor } = await reports.replies(report);
for (const r of items) console.log(r.sender, r.body);
await reports.write(report, "Reinstalling did not help");
```

### Electron: wiring

```js
// main.js
const { app, ipcMain } = require("electron");
const { join } = require("node:path");
const { readFile, writeFile } = require("node:fs/promises");

const store = join(app.getPath("userData"), "tolkovo-reports.json");
const load = () => readFile(store, "utf8").then(JSON.parse).catch(() => []);
const save = (list) => writeFile(store, JSON.stringify(list), { mode: 0o600 });

app.whenReady().then(async () => {
  const { TolkovoReports } = await import("./tolkovo-report.mjs");
  const reports = new TolkovoReports({ baseUrl: "https://support.example.com", key: "tk_ingest_..." });

  ipcMain.handle("report:send", async (_e, { message, screenshotPath, identity }) => {
    const r = await reports.send({
      message,
      app: { name: app.getName(), version: app.getVersion(), os: process.platform, os_version: process.getSystemVersion() },
      contact: identity, // { external_id, name, email, identity_token } from your backend, or undefined
      files: [join(app.getPath("logs"), "main.log"), screenshotPath].filter(Boolean),
    });
    await save([...(await load()), { ...r, cursor: "" }]);
    return r.id;
  });

  ipcMain.handle("report:replies", async () => {
    const list = await load();
    const answers = [];
    for (const r of list) {
      const page = await reports.replies(r, r.cursor);
      r.cursor = page.cursor;
      answers.push(...page.items.map((m) => ({ report: r.id, ...m })));
    }
    await save(list);
    return answers;
  });

  ipcMain.handle("report:write", async (_e, { id, message }) => {
    const r = (await load()).find((x) => x.id === id);
    return reports.write(r, message);
  });
});
```

Expose the three calls to the renderer through a preload script with `contextBridge`, and call `report:replies` at start and then at most once a minute while the report window is open.

A screenshot of the app window: `const img = await win.webContents.capturePage(); await writeFile(path, img.toPNG());`.

## 5. Signed-in users

When the user is signed in, let your **backend** sign their id with the identity secret and send it in `contact`:

```js
// your backend, not the app
import { createHmac } from "node:crypto";
const identity_token = createHmac("sha256", process.env.TOLKOVO_IDENTITY_SECRET).update(user.id).digest("hex");
// → the app gets { external_id: user.id, name, email, identity_token } after sign-in
```

With a valid token the operator sees "identity confirmed", and the report joins the user's customer card: earlier reports and chats from the website widget with the same `external_id`. Without a token, or with a wrong one, the report still arrives, from an unconfirmed contact (`contact_verified: false`); it is never an error.

## 6. C#

```csharp
using System.Net.Http.Json;
using System.Security.Cryptography;
using System.Text.Json;

var http = new HttpClient { BaseAddress = new Uri("https://support.example.com/api/v1/") };
http.DefaultRequestHeaders.Authorization = new("Bearer", "tk_ingest_...");

var log = await File.ReadAllBytesAsync("zerno.log");
var create = new HttpRequestMessage(HttpMethod.Post, "ingest/reports")
{
    Content = JsonContent.Create(new
    {
        message = "After sign-in the cart is empty",
        app = new { name = "Zerno", version = "2.4.1", os = "Windows", os_version = Environment.OSVersion.VersionString },
        files = new[] { new { name = "zerno.log", size = log.Length, content_type = "text/plain",
                              sha256 = Convert.ToHexString(SHA256.HashData(log)).ToLowerInvariant() } },
    }),
};
create.Headers.Add("Idempotency-Key", Guid.NewGuid().ToString());
using var created = JsonDocument.Parse(await (await http.SendAsync(create)).EnsureSuccessStatusCode().Content.ReadAsStringAsync());
var id = created.RootElement.GetProperty("report_id").GetString();
var token = created.RootElement.GetProperty("report_token").GetString();

var upload = created.RootElement.GetProperty("uploads")[0].GetProperty("upload");
var put = new HttpRequestMessage(new HttpMethod(upload.GetProperty("method").GetString()!), upload.GetProperty("url").GetString())
{
    Content = new ByteArrayContent(log),
};
foreach (var h in upload.GetProperty("headers").EnumerateObject())
{
    if (h.Name.Equals("Content-Length", StringComparison.OrdinalIgnoreCase)) continue; // set from the content
    if (!put.Headers.TryAddWithoutValidation(h.Name, h.Value.GetString()))
        put.Content.Headers.TryAddWithoutValidation(h.Name, h.Value.GetString()); // Content-Type
}
(await new HttpClient().SendAsync(put)).EnsureSuccessStatusCode();

var complete = new HttpRequestMessage(HttpMethod.Post, $"ingest/reports/{id}/complete");
complete.Headers.Add("X-Report-Token", token);
(await http.SendAsync(complete)).EnsureSuccessStatusCode();
// Keep id and token; read GET ingest/reports/{id}/replies with the same X-Report-Token.
```

Upload with a separate `HttpClient` (or a request without the default `Authorization` header): the upload link carries its own signature.

## 7. Privacy

- Reports carry what the app puts in them. Do not put passwords, tokens or payment data in logs you attach.
- Files are kept for 30 days by default (the admin sets 1–365 under **Keys and limits**), then deleted; the conversation stays.
- The replies the app reads hold no names of staff and no internal notes.
