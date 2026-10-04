// Confirm or delete a waitlist address with the token from the URL fragment
// (POST /api/v1/waitlist/confirm | /unsubscribe). Runs only on a button press.

interface Messages {
  button: string;
  sending: string;
  errNoToken: string;
  errInvalid: string;
  errRateLimited: string;
  errUnavailable: string;
}

const KEYS: ReadonlyArray<keyof Messages> = [
  "button",
  "sending",
  "errNoToken",
  "errInvalid",
  "errRateLimited",
  "errUnavailable",
];
const TIMEOUT_MS = 10_000;
// 32 random bytes in unpadded base64url, as issued by the server.
const TOKEN_RE = /^[A-Za-z0-9_-]{43}$/;

function parseMessages(raw: string | undefined): Messages | null {
  if (!raw) return null;
  try {
    const v: unknown = JSON.parse(raw);
    if (typeof v !== "object" || v === null) return null;
    const rec = v as Record<string, unknown>;
    return KEYS.every((k) => typeof rec[k] === "string") ? (rec as unknown as Messages) : null;
  } catch {
    return null;
  }
}

type Outcome = "ok" | "invalid" | "rate_limited" | "unavailable";

async function post(endpoint: string, token: string): Promise<Outcome> {
  const ctrl = new AbortController();
  const timer = setTimeout(() => ctrl.abort(), TIMEOUT_MS);
  try {
    const res = await fetch(endpoint, {
      method: "POST",
      headers: { "Content-Type": "application/json", Accept: "application/json, application/problem+json" },
      body: JSON.stringify({ token }),
      credentials: "omit",
      referrerPolicy: "no-referrer",
      signal: ctrl.signal,
    });
    if (res.ok) return "ok";
    if (res.status === 400 || res.status === 404) return "invalid";
    if (res.status === 429) return "rate_limited";
    return "unavailable";
  } catch {
    return "unavailable";
  } finally {
    clearTimeout(timer);
  }
}

function init(box: HTMLElement): void {
  const m = parseMessages(box.dataset["messages"]);
  const endpoint = box.dataset["endpoint"];
  const button = document.getElementById("wla-go");
  const status = document.getElementById("wla-status");
  const done = document.getElementById("wla-done");
  if (!m || !endpoint || !(button instanceof HTMLButtonElement) || !status || !done) return;

  // Base64url needs no decoding; anything else fails TOKEN_RE below.
  const token = location.hash.slice(1);
  // Keep the token out of the address bar, history sync and screenshots.
  if (location.hash) history.replaceState(null, "", location.pathname + location.search);

  const showError = (text: string) => {
    status.textContent = text;
    status.classList.toggle("error", text !== "");
  };

  if (!TOKEN_RE.test(token)) {
    button.hidden = true;
    showError(m.errNoToken);
    return;
  }

  let busy = false;
  button.addEventListener("click", () => {
    if (busy) return;
    busy = true;
    showError("");
    button.setAttribute("aria-disabled", "true");
    button.textContent = m.sending;
    void post(endpoint, token).then((outcome) => {
      busy = false;
      button.removeAttribute("aria-disabled");
      button.textContent = m.button;
      switch (outcome) {
        case "ok":
          box.hidden = true;
          done.hidden = false;
          done.focus();
          break;
        case "invalid":
          button.hidden = true;
          showError(m.errInvalid);
          break;
        case "rate_limited":
          showError(m.errRateLimited);
          break;
        case "unavailable":
          showError(m.errUnavailable);
          break;
      }
    });
  });
}

const box = document.getElementById("wla");
if (box) init(box);

// A module, so its names do not clash with other page scripts in the global scope.
export {};
