import { useEffect, useLayoutEffect, useRef, useState } from "preact/hooks";

import type { Controller, State } from "../controller";
import type { Translate } from "../i18n";
import type { ChatMessage, Locale } from "../types";

/** What the UI shows besides the state: texts and looks from Tolkovo.init(). */
export interface View {
  locale: Locale;
  t: Translate;
  title: string | undefined;
  subtitle: string | undefined;
  greeting: string | undefined;
  placeholder: string | undefined;
  logo: string | undefined;
  position: "bottom-right" | "bottom-left";
}

function useStore(c: Controller): State {
  const [state, setState] = useState(c.get());
  useEffect(() => {
    setState(c.get());
    return c.subscribe(() => setState(c.get()));
  }, [c]);
  return state;
}

const ChatIcon = () => (
  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
    <path d="M21 12a8 8 0 0 1-11.6 7.1L4 20l1.1-4.6A8 8 0 1 1 21 12Z" />
  </svg>
);

const CloseIcon = () => (
  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true">
    <path d="M6 6l12 12M18 6 6 18" />
  </svg>
);

const SendIcon = () => (
  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
    <path d="M12 19V5M5 12l7-7 7 7" />
  </svg>
);

/** An image is shown only from http(s) or an inline image. */
function safeImage(src: string | undefined): string | undefined {
  return src && /^(https?:|data:image\/)/i.test(src) ? src : undefined;
}

export function App({ c, view }: { c: Controller; view: View }) {
  const s = useStore(c);
  const { t } = view;
  const launcherRef = useRef<HTMLButtonElement>(null);
  const wasOpenRef = useRef(s.open);

  // Closing returns the focus to the launcher, as for any dialog.
  useEffect(() => {
    if (wasOpenRef.current && !s.open) launcherRef.current?.focus();
    wasOpenRef.current = s.open;
  }, [s.open]);

  const label = s.unread > 0 ? `${t("open")}. ${t("unread", { n: s.unread })}` : t("open");
  return (
    <div class="tk-root" data-position={view.position} data-open={s.open ? "" : undefined}>
      {s.open ? <Window c={c} s={s} view={view} /> : null}
      {s.launcher ? (
        <button
          ref={launcherRef}
          type="button"
          class="tk-launcher"
          aria-label={s.open ? t("close") : label}
          aria-expanded={s.open}
          onClick={() => c.setOpen(!s.open)}
        >
          {s.open ? <CloseIcon /> : <ChatIcon />}
          {!s.open && s.unread > 0 ? (
            <span class="tk-badge" aria-hidden="true">
              {s.unread > 99 ? "99+" : s.unread}
            </span>
          ) : null}
        </button>
      ) : null}
    </div>
  );
}

function Window({ c, s, view }: { c: Controller; s: State; view: View }) {
  const { t } = view;
  const title = view.title ?? (s.channelName || t("title"));
  const logo = safeImage(view.logo);
  return (
    <section
      class="tk-window"
      role="dialog"
      aria-label={title}
      onKeyDown={(e) => {
        if (e.key === "Escape") c.setOpen(false);
      }}
    >
      <header class="tk-header">
        {logo ? <img class="tk-logo" src={logo} alt="" /> : null}
        <div class="tk-heading">
          <h2 class="tk-title">{title}</h2>
          {view.subtitle ? <p class="tk-subtitle">{view.subtitle}</p> : null}
        </div>
        <button type="button" class="tk-icon-button" aria-label={t("close")} onClick={() => c.setOpen(false)}>
          <CloseIcon />
        </button>
      </header>
      <Log c={c} s={s} view={view} />
      {s.status === "unavailable" ? (
        <p class="tk-notice" role="status">
          {t("unavailable")}
        </p>
      ) : !s.online ? (
        <p class="tk-notice" role="status">
          {t("offline")}
        </p>
      ) : null}
      <Composer c={c} view={view} disabled={s.status === "unavailable"} />
    </section>
  );
}

function useTime(locale: Locale) {
  const fmt = new Intl.DateTimeFormat(locale, { hour: "2-digit", minute: "2-digit" });
  return (iso: string) => fmt.format(new Date(iso));
}

function Log({ c, s, view }: { c: Controller; s: State; view: View }) {
  const { t } = view;
  const time = useTime(view.locale);
  const logRef = useRef<HTMLOListElement>(null);
  const count = s.messages.length + s.pending.length + (s.typing ? 1 : 0);

  // New messages scroll into view.
  useLayoutEffect(() => {
    const el = logRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [count]);

  return (
    <ol ref={logRef} class="tk-log" aria-label={t("log")} aria-live="polite" aria-relevant="additions">
      {view.greeting ? (
        <li class="tk-msg" data-from="operator">
          <div class="tk-bubble">{view.greeting}</div>
        </li>
      ) : null}
      {s.messages.map((m) => (
        <Message key={m.id} m={m} time={time(m.createdAt)} t={t} />
      ))}
      {s.pending.map((p) => (
        <li key={p.key} class="tk-msg" data-from="contact" data-pending="">
          <span class="tk-sr">{t("you")}:</span>
          <div class="tk-bubble">{p.body}</div>
          {p.failed ? (
            <span class="tk-failed" role="alert">
              {t("failed")}
              <button type="button" class="tk-link-button" onClick={() => void c.retry(p.key)}>
                {t("retry")}
              </button>
            </span>
          ) : (
            <span class="tk-meta">{t("sending")}</span>
          )}
        </li>
      ))}
      {s.typing ? <li class="tk-typing">{t("typing")}</li> : null}
    </ol>
  );
}

function Message({ m, time, t }: { m: ChatMessage; time: string; t: Translate }) {
  return (
    <li class="tk-msg" data-from={m.sender}>
      <span class="tk-sr">{m.sender === "contact" ? t("you") : t("support")}:</span>
      <div class="tk-bubble">{m.body}</div>
      <span class="tk-meta">{time}</span>
    </li>
  );
}

function Composer({ c, view, disabled }: { c: Controller; view: View; disabled: boolean }) {
  const { t } = view;
  const [text, setText] = useState("");
  const inputRef = useRef<HTMLTextAreaElement>(null);

  useEffect(() => {
    inputRef.current?.focus();
  }, []);

  // The field grows with the text up to its max-height.
  useLayoutEffect(() => {
    const el = inputRef.current;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = `${el.scrollHeight}px`;
  }, [text]);

  const tooLong = text.length > 10_000;
  const send = () => {
    if (!text.trim() || tooLong || disabled) return;
    void c.send(text);
    setText("");
  };

  return (
    <form
      class="tk-composer"
      onSubmit={(e) => {
        e.preventDefault();
        send();
      }}
    >
      <label class="tk-sr" for="tk-input">
        {view.placeholder ?? t("placeholder")}
      </label>
      <textarea
        id="tk-input"
        ref={inputRef}
        class="tk-input"
        rows={1}
        value={text}
        disabled={disabled}
        placeholder={view.placeholder ?? t("placeholder")}
        aria-invalid={tooLong || undefined}
        aria-describedby={tooLong ? "tk-too-long" : undefined}
        onInput={(e) => setText(e.currentTarget.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter" && !e.shiftKey && !e.isComposing) {
            e.preventDefault();
            send();
          }
        }}
      />
      {tooLong ? (
        <span id="tk-too-long" class="tk-sr" role="alert">
          {t("tooLong")}
        </span>
      ) : null}
      <button type="submit" class="tk-send" aria-label={t("send")} disabled={disabled || !text.trim() || tooLong}>
        <SendIcon />
      </button>
    </form>
  );
}
