// Waitlist form: client-side validation and submission to POST /api/v1/waitlist.
// The server sends the double opt-in email; until the address is confirmed it is not on the list.

interface Messages {
  sending: string;
  submit: string;
  successText: string;
  errEmailEmpty: string;
  errEmailFormat: string;
  errConsent: string;
  errUnavailable: string;
  errRateLimited: string;
  errInvalid: string;
  errFallbackLink: string;
  fallbackUrl: string;
}

const MESSAGE_KEYS: ReadonlyArray<keyof Messages> = [
  "sending",
  "submit",
  "successText",
  "errEmailEmpty",
  "errEmailFormat",
  "errConsent",
  "errUnavailable",
  "errRateLimited",
  "errInvalid",
  "errFallbackLink",
  "fallbackUrl",
];

const TIMEOUT_MS = 10_000;
// Deliberately loose: the server is the authority, this only catches obvious typos.
const EMAIL_RE = /^[^\s@]+@[^\s@]+\.[^\s@]{2,}$/;

function parseMessages(raw: string | undefined): Messages | null {
  if (!raw) return null;
  try {
    const v: unknown = JSON.parse(raw);
    if (typeof v !== "object" || v === null) return null;
    const rec = v as Record<string, unknown>;
    return MESSAGE_KEYS.every((k) => typeof rec[k] === "string") ? (rec as unknown as Messages) : null;
  } catch {
    return null;
  }
}

function newKey(): string {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") return crypto.randomUUID();
  return `${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`;
}

type Outcome = "ok" | "invalid" | "rate_limited" | "unavailable";

async function submit(body: object, idempotencyKey: string, endpoint: string): Promise<Outcome> {
  const ctrl = new AbortController();
  const timer = setTimeout(() => ctrl.abort(), TIMEOUT_MS);
  try {
    const res = await fetch(endpoint, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Accept: "application/json, application/problem+json",
        "Idempotency-Key": idempotencyKey,
      },
      body: JSON.stringify(body),
      credentials: "omit",
      signal: ctrl.signal,
    });
    if (res.ok) return "ok";
    if (res.status === 400 || res.status === 422) return "invalid";
    if (res.status === 429) return "rate_limited";
    // 404/405 from a static host without the API, 5xx, gateway errors.
    return "unavailable";
  } catch {
    return "unavailable";
  } finally {
    clearTimeout(timer);
  }
}

function init(form: HTMLFormElement): void {
  const m = parseMessages(form.dataset["messages"]);
  const email = form.querySelector<HTMLInputElement>("#wl-email");
  const consent = form.querySelector<HTMLInputElement>("#wl-consent");
  const emailErr = form.querySelector<HTMLElement>("#wl-email-err");
  const consentErr = form.querySelector<HTMLElement>("#wl-consent-err");
  const status = form.querySelector<HTMLElement>("#wl-status");
  const button = form.querySelector<HTMLButtonElement>('button[type="submit"]');
  const honeypot = form.querySelector<HTMLInputElement>("#wl-website");
  const done = document.getElementById("wl-done");
  const doneText = document.getElementById("wl-done-text");
  if (!m || !email || !consent || !emailErr || !consentErr || !status || !button || !done || !doneText) return;

  const locale = form.dataset["locale"] === "en" ? "en" : "ru";
  const endpoint = form.getAttribute("action") ?? "/api/v1/waitlist";
  let busy = false;
  // Same key for retries of the same address, so a retry after a timeout cannot create a duplicate.
  let key = newKey();
  let keyFor = "";

  const setError = (input: HTMLInputElement, box: HTMLElement, text: string) => {
    box.textContent = text;
    if (text) input.setAttribute("aria-invalid", "true");
    else input.removeAttribute("aria-invalid");
  };

  const setStatus = (text: string, withFallback: boolean) => {
    status.replaceChildren();
    status.classList.toggle("error", text !== "");
    if (!text) return;
    status.append(text);
    if (withFallback) {
      const a = document.createElement("a");
      a.href = m.fallbackUrl;
      a.textContent = m.errFallbackLink;
      status.append(" ", a);
    }
  };

  const validate = (): boolean => {
    const value = email.value.trim();
    let emailMsg = "";
    if (!value) emailMsg = m.errEmailEmpty;
    else if (value.length > 254 || !EMAIL_RE.test(value) || !email.checkValidity()) emailMsg = m.errEmailFormat;
    setError(email, emailErr, emailMsg);
    const consentMsg = consent.checked ? "" : m.errConsent;
    setError(consent, consentErr, consentMsg);
    if (emailMsg) email.focus();
    else if (consentMsg) consent.focus();
    return !emailMsg && !consentMsg;
  };

  const setBusy = (on: boolean) => {
    busy = on;
    form.setAttribute("aria-busy", String(on));
    button.setAttribute("aria-disabled", String(on));
    button.textContent = on ? m.sending : m.submit;
  };

  const showDone = (address: string) => {
    doneText.textContent = m.successText.replace("{email}", address);
    form.hidden = true;
    done.hidden = false;
    done.focus();
  };

  email.addEventListener("input", () => {
    if (email.hasAttribute("aria-invalid")) setError(email, emailErr, "");
  });
  consent.addEventListener("change", () => {
    if (consent.checked) setError(consent, consentErr, "");
  });

  form.addEventListener("submit", (ev) => {
    ev.preventDefault();
    if (busy) return;
    setStatus("", false);
    if (!validate()) return;

    const address = email.value.trim();
    // Bots fill hidden fields; pretend success and send nothing.
    if (honeypot && honeypot.value !== "") {
      showDone(address);
      return;
    }
    if (keyFor !== address) {
      key = newKey();
      keyFor = address;
    }
    const channels = [...form.querySelectorAll<HTMLInputElement>('input[name="channels"]:checked')].map(
      (c) => c.value,
    );

    setBusy(true);
    void submit({ email: address, locale, consent: true, channels }, key, endpoint).then((outcome) => {
      setBusy(false);
      switch (outcome) {
        case "ok":
          showDone(address);
          break;
        case "invalid":
          setError(email, emailErr, m.errInvalid);
          email.focus();
          break;
        case "rate_limited":
          setStatus(m.errRateLimited, false);
          break;
        case "unavailable":
          setStatus(m.errUnavailable, true);
          break;
      }
    });
  });
}

const form = document.getElementById("wl");
if (form instanceof HTMLFormElement) init(form);
