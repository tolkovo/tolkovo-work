import { useEffect, useLayoutEffect, useRef, useState } from "preact/hooks";

import type { Controller, OutFile, State } from "../controller";
import { ACCEPT, MAX_FILE_SIZE, MAX_FILES, fileType, formatSize, isImage } from "../files";
import type { Translate } from "../i18n";
import type { ChatFile, ChatMessage, Locale, PrechatField } from "../types";

/** The pre-chat or the offline form from Tolkovo.init(), checked (widget.tsx). */
export interface PrechatView {
  fields: PrechatField[];
  required: PrechatField[];
  intro: string | undefined;
  consent: { policyUrl: string; text: string | undefined } | undefined;
}

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
  prechat: PrechatView | undefined;
  offline: PrechatView | undefined;
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

const ClipIcon = () => (
  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
    <path d="m21 11-8.6 8.6a5 5 0 0 1-7-7l8.5-8.6a3.3 3.3 0 0 1 4.7 4.7l-8.6 8.6a1.7 1.7 0 0 1-2.3-2.3l7.9-8" />
  </svg>
);

const FileIcon = () => (
  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
    <path d="M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8Z" />
    <path d="M14 3v5h5" />
  </svg>
);

const StarIcon = () => (
  <svg viewBox="0 0 24 24" stroke-width="1.6" stroke-linejoin="round" aria-hidden="true">
    <path d="m12 3.2 2.7 5.5 6 .9-4.3 4.2 1 6-5.4-2.8-5.4 2.8 1-6-4.3-4.2 6-.9Z" />
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

/** Files chosen for the next message, checked as the server would; the first refusal is explained. */
function useDraftFiles(t: Translate) {
  const [files, setFiles] = useState<File[]>([]);
  const [error, setError] = useState("");
  const add = (list: File[]) => {
    const next = [...files];
    let why = "";
    for (const f of list) {
      if (next.length >= MAX_FILES) {
        why = t("tooManyFiles");
        break;
      }
      if (!fileType(f) || f.size === 0) why = t("fileType", { name: f.name });
      else if (f.size > MAX_FILE_SIZE) why = t("fileTooBig", { name: f.name });
      else next.push(f);
    }
    setFiles(next);
    setError(why);
  };
  const remove = (f: File) => {
    setFiles(files.filter((x) => x !== f));
    setError("");
  };
  const clear = () => {
    setFiles([]);
    setError("");
  };
  return { files, error, add, remove, clear };
}

type Draft = ReturnType<typeof useDraftFiles>;

function Window({ c, s, view }: { c: Controller; s: State; view: View }) {
  const { t } = view;
  const title = view.title ?? (s.channelName || t("title"));
  const logo = safeImage(view.logo);
  const draft = useDraftFiles(t);
  const [dragging, setDragging] = useState(false);
  // Nobody is there and the visitor has not written: a message and a contact instead of the chat.
  const leaving =
    !!view.offline && s.status === "ready" && !s.operatorsOnline && s.pending.length === 0 && !s.messages.some((m) => m.sender === "contact");
  const asking = !leaving && s.intro && !!view.prechat && s.status !== "unavailable";
  const canAttach = s.status !== "unavailable" && !asking && !leaving;
  const hasFiles = (e: DragEvent) => Array.from(e.dataTransfer?.types ?? []).includes("Files");
  return (
    <section
      class="tk-window"
      role="dialog"
      aria-label={title}
      onKeyDown={(e) => {
        if (e.key === "Escape") c.setOpen(false);
      }}
      onDragOver={(e) => {
        if (!canAttach || !hasFiles(e)) return;
        e.preventDefault();
        setDragging(true);
      }}
      onDragLeave={(e) => {
        if (!e.currentTarget.contains(e.relatedTarget as Node | null)) setDragging(false);
      }}
      onDrop={(e) => {
        if (!canAttach || !hasFiles(e)) return;
        e.preventDefault();
        setDragging(false);
        draft.add(Array.from(e.dataTransfer?.files ?? []));
      }}
    >
      {dragging ? (
        <div class="tk-drop" aria-hidden="true">
          {t("drop")}
        </div>
      ) : null}
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
      ) : s.status === "ready" && !s.operatorsOnline && !leaving ? (
        <p class="tk-notice" data-kind="away" role="status">
          {t("away")}
        </p>
      ) : null}
      {leaving ? (
        <ContactForm key="offline" c={c} view={view} form={view.offline!} offline />
      ) : asking ? (
        <ContactForm key="prechat" c={c} view={view} form={view.prechat!} />
      ) : (
        <Composer c={c} view={view} draft={draft} disabled={!canAttach} />
      )}
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
  // A resolved conversation asks for a rating under the last message.
  const rating = s.conversation?.resolved && s.pending.length === 0 ? s.conversation : null;
  const count = s.messages.length + s.pending.length + (s.typing ? 1 : 0) + (rating ? 1 : 0);

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
        <Message key={m.id} m={m} time={time(m.createdAt)} view={view} onBroken={() => void c.renewFiles()} />
      ))}
      {s.pending.map((p) => (
        <li key={p.key} class="tk-msg" data-from="contact" data-pending="">
          <span class="tk-sr">{t("you")}:</span>
          {p.files.length > 0 ? <PendingFiles files={p.files} view={view} /> : null}
          {p.body ? <div class="tk-bubble">{p.body}</div> : null}
          {p.failed ? (
            <span class="tk-failed" role="alert">
              {p.error === "rejected" ? t("fileRejected", { name: p.rejected ?? "" }) : p.error === "unavailable" ? t("scanUnavailable") : t("failed")}
              {p.body || p.files.length > 0 ? (
                <button type="button" class="tk-link-button" onClick={() => void c.retry(p.key)}>
                  {t("retry")}
                </button>
              ) : null}
              <button type="button" class="tk-link-button" onClick={() => c.dismiss(p.key)}>
                {t("dismiss")}
              </button>
            </span>
          ) : (
            <span class="tk-meta">{t("sending")}</span>
          )}
        </li>
      ))}
      {rating ? <Rating key={rating.id} c={c} score={rating.rating?.score ?? 0} view={view} /> : null}
      {s.typing ? <li class="tk-typing">{t("typing")}</li> : null}
    </ol>
  );
}

function Message({ m, time, view, onBroken }: { m: ChatMessage; time: string; view: View; onBroken: () => void }) {
  const { t } = view;
  return (
    <li class="tk-msg" data-from={m.sender}>
      <span class="tk-sr">{m.sender === "contact" ? t("you") : t("support")}:</span>
      {m.files.length > 0 ? <Files files={m.files} view={view} onBroken={onBroken} /> : null}
      {m.body ? <div class="tk-bubble">{m.body}</div> : null}
      <span class="tk-meta">{time}</span>
    </li>
  );
}

const SCORES = [1, 2, 3, 4, 5];

/** Stars from 1 to 5 and a comment; once rated, thanks and the score. */
function Rating({ c, score: rated, view }: { c: Controller; score: number; view: View }) {
  const { t } = view;
  const [score, setScore] = useState(0);
  const [hover, setHover] = useState(0);
  const [comment, setComment] = useState("");
  const [busy, setBusy] = useState(false);
  const [failed, setFailed] = useState(false);
  const groupRef = useRef<HTMLDivElement>(null);

  if (rated > 0) {
    return (
      <li class="tk-rate">
        <p class="tk-rate-title">{t("rateThanks")}</p>
        <span class="tk-stars" role="img" aria-label={t("rateYours", { n: rated })}>
          {SCORES.map((n) => (
            <span key={n} class="tk-star" data-on={n <= rated ? "" : undefined}>
              <StarIcon />
            </span>
          ))}
        </span>
      </li>
    );
  }

  // One tab stop for the stars; the arrows move between them, as in any radio group.
  const choose = (n: number, focus = false) => {
    setScore(n);
    if (focus) groupRef.current?.querySelectorAll<HTMLButtonElement>("button")[n - 1]?.focus();
  };
  const submit = async () => {
    if (!score || busy) return;
    setBusy(true);
    setFailed(false);
    try {
      await c.rate(score, comment);
    } catch {
      setFailed(true);
      setBusy(false);
    }
  };
  const shown = hover || score;
  return (
    <li class="tk-rate">
      <form
        class="tk-rate-form"
        aria-busy={busy || undefined}
        onSubmit={(e) => {
          e.preventDefault();
          void submit();
        }}
      >
        <p id="tk-rate-title" class="tk-rate-title">
          {t("rateTitle")}
        </p>
        <div
          ref={groupRef}
          class="tk-stars"
          role="radiogroup"
          aria-labelledby="tk-rate-title"
          onMouseLeave={() => setHover(0)}
          onKeyDown={(e) => {
            const step = e.key === "ArrowRight" || e.key === "ArrowUp" ? 1 : e.key === "ArrowLeft" || e.key === "ArrowDown" ? -1 : 0;
            if (!step) return;
            e.preventDefault();
            choose(Math.min(5, Math.max(1, (score || 0) + step)), true);
          }}
        >
          {SCORES.map((n) => (
            <button
              key={n}
              type="button"
              role="radio"
              class="tk-star"
              aria-checked={score === n}
              aria-label={t("rateStar", { n })}
              tabIndex={(score || 1) === n ? 0 : -1}
              data-on={n <= shown ? "" : undefined}
              disabled={busy}
              onMouseEnter={() => setHover(n)}
              onClick={() => choose(n)}
            >
              <StarIcon />
            </button>
          ))}
        </div>
        {score > 0 ? (
          <>
            <label class="tk-sr" for="tk-rate-comment">
              {t("rateComment")}
            </label>
            <textarea
              id="tk-rate-comment"
              class="tk-text tk-textarea"
              rows={2}
              maxLength={1000}
              placeholder={t("rateComment")}
              value={comment}
              disabled={busy}
              onInput={(e) => setComment(e.currentTarget.value)}
            />
            {failed ? (
              <p class="tk-file-error" role="alert">
                {t("prechatFailed")}
              </p>
            ) : null}
            <button type="submit" class="tk-start" disabled={busy}>
              {t("rateSend")}
            </button>
          </>
        ) : null}
      </form>
    </li>
  );
}

/** Pictures as previews, other files as a name and a size. Links open in a new tab; the server sends files as downloads. */
function Files({ files, view, onBroken }: { files: ChatFile[]; view: View; onBroken: () => void }) {
  return (
    <div class="tk-files">
      {files.map((f, i) =>
        isImage(f.contentType) ? (
          <a key={i} class="tk-thumb" href={f.url} target="_blank" rel="noopener noreferrer" title={f.name}>
            <img src={f.url} alt={view.t("image", { name: f.name })} loading="lazy" onError={onBroken} />
          </a>
        ) : (
          <a key={i} class="tk-file" href={f.url} target="_blank" rel="noopener noreferrer">
            <FileIcon />
            <span class="tk-file-name">{f.name}</span>
            <span class="tk-file-size">{formatSize(f.size, view.locale)}</span>
          </a>
        ),
      )}
    </div>
  );
}

function PendingFiles({ files, view }: { files: OutFile[]; view: View }) {
  return (
    <div class="tk-files">
      {files.map((f, i) =>
        f.preview ? (
          <span key={i} class="tk-thumb">
            <img src={f.preview} alt={view.t("image", { name: f.name })} />
          </span>
        ) : (
          <span key={i} class="tk-file">
            <FileIcon />
            <span class="tk-file-name">{f.name}</span>
            <span class="tk-file-size">{formatSize(f.size, view.locale)}</span>
          </span>
        ),
      )}
    </div>
  );
}

/** A chosen file above the field; a picture shows its preview. */
function Chip({ file, view, onRemove }: { file: File; view: View; onRemove: () => void }) {
  const [preview, setPreview] = useState<string>();
  useEffect(() => {
    if (!isImage(fileType(file) ?? "")) return;
    const url = URL.createObjectURL(file);
    setPreview(url);
    return () => URL.revokeObjectURL(url);
  }, [file]);
  return (
    <li class="tk-chip">
      {preview ? <img src={preview} alt="" /> : <FileIcon />}
      <span class="tk-file-name">{file.name}</span>
      <span class="tk-file-size">{formatSize(file.size, view.locale)}</span>
      <button type="button" class="tk-chip-remove" aria-label={view.t("remove", { name: file.name })} onClick={onRemove}>
        <CloseIcon />
      </button>
    </li>
  );
}

function Composer({ c, view, draft, disabled }: { c: Controller; view: View; draft: Draft; disabled: boolean }) {
  const { t } = view;
  const [text, setText] = useState("");
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const pickerRef = useRef<HTMLInputElement>(null);

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
  const empty = !text.trim() && draft.files.length === 0;
  const send = () => {
    if (empty || tooLong || disabled) return;
    void c.send(text, draft.files);
    setText("");
    draft.clear();
  };

  return (
    <form
      class="tk-composer"
      onSubmit={(e) => {
        e.preventDefault();
        send();
      }}
    >
      {draft.files.length > 0 ? (
        <ul class="tk-chips">
          {draft.files.map((f, i) => (
            <Chip key={i} file={f} view={view} onRemove={() => draft.remove(f)} />
          ))}
        </ul>
      ) : null}
      {draft.error ? (
        <p class="tk-file-error" role="alert">
          {draft.error}
        </p>
      ) : null}
      <input
        ref={pickerRef}
        type="file"
        class="tk-sr"
        tabIndex={-1}
        aria-hidden="true"
        multiple
        accept={ACCEPT}
        onChange={(e) => {
          draft.add(Array.from(e.currentTarget.files ?? []));
          e.currentTarget.value = "";
        }}
      />
      <button type="button" class="tk-attach" aria-label={t("attach")} title={t("attach")} disabled={disabled} onClick={() => pickerRef.current?.click()}>
        <ClipIcon />
      </button>
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
        onPaste={(e) => {
          const pasted = Array.from(e.clipboardData?.files ?? []);
          if (pasted.length === 0) return;
          e.preventDefault();
          draft.add(pasted);
        }}
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
      <button type="submit" class="tk-send" aria-label={t("send")} disabled={disabled || empty || tooLong}>
        <SendIcon />
      </button>
    </form>
  );
}

/** The same checks as the server (internal/conversations). */
const EMAIL = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;
const PHONE = /^\+?[0-9][0-9 ()-]{1,30}$/;
const LIMITS: Record<PrechatField, number> = { name: 200, email: 254, phone: 32 };
const INPUT: Record<PrechatField, { type: "text" | "email" | "tel"; autocomplete: string }> = {
  name: { type: "text", autocomplete: "name" },
  email: { type: "email", autocomplete: "email" },
  phone: { type: "tel", autocomplete: "tel" },
};

type FormErrors = Partial<Record<PrechatField | "consent" | "message", string>>;

/**
 * Who the visitor is, instead of the message field: before the first message (the pre-chat form),
 * or with the message itself while nobody is there (offline), when the email or the phone is a must.
 */
function ContactForm({ c, view, form, offline = false }: { c: Controller; view: View; form: PrechatView; offline?: boolean }) {
  const { t } = view;
  const [values, setValues] = useState<Record<PrechatField, string>>({ name: "", email: "", phone: "" });
  const [message, setMessage] = useState("");
  const [agreed, setAgreed] = useState(false);
  const [errors, setErrors] = useState<FormErrors>({});
  const [busy, setBusy] = useState(false);
  const [failed, setFailed] = useState(false);
  const formRef = useRef<HTMLFormElement>(null);

  useEffect(() => {
    formRef.current?.querySelector<HTMLInputElement>("input")?.focus();
  }, []);

  const check = (): FormErrors => {
    const e: FormErrors = {};
    for (const f of form.fields) {
      const v = values[f].trim();
      if (!v) {
        if (form.required.includes(f)) e[f] = t("required");
      } else if (f === "email" && !EMAIL.test(v)) e[f] = t("badEmail");
      else if (f === "phone" && (!PHONE.test(v) || v.length < 3)) e[f] = t("badPhone");
    }
    if (offline) {
      const reach = form.fields.filter((f) => f !== "name");
      if (!reach.some((f) => values[f].trim()) && !reach.some((f) => e[f])) e[reach[0]!] = t("needReach");
      if (!message.trim()) e.message = t("required");
      else if (message.length > 10_000) e.message = t("tooLong");
    }
    if (form.consent && !agreed) e.consent = t(offline ? "needConsentToSend" : "needConsent");
    return e;
  };

  const submit = async () => {
    const e = check();
    setErrors(e);
    // The first field in the form's order with a problem takes the focus.
    const first = [...form.fields, "message", "consent"].find((f) => e[f as keyof FormErrors]);
    if (first) {
      formRef.current?.querySelector<HTMLElement>(`#tk-pre-${first}`)?.focus();
      return;
    }
    const body: Record<string, unknown> = {};
    for (const f of form.fields) if (values[f].trim()) body[f] = values[f].trim();
    if (form.consent) body.consent = { policy_url: form.consent.policyUrl };
    setBusy(true);
    setFailed(false);
    try {
      await (offline ? c.leaveMessage(body, message) : c.introduce(body));
    } catch {
      setFailed(true);
      setBusy(false);
    }
  };

  return (
    <form
      ref={formRef}
      class="tk-prechat"
      noValidate
      aria-busy={busy || undefined}
      onSubmit={(e) => {
        e.preventDefault();
        void submit();
      }}
    >
      <p class="tk-prechat-intro">{form.intro ?? t(offline ? "offlineIntro" : "prechatIntro")}</p>
      {form.fields.map((f) => {
        const required = form.required.includes(f);
        return (
          <div key={f} class="tk-field">
            <label for={`tk-pre-${f}`}>
              {t(f)}
              {required ? <span aria-hidden="true"> *</span> : null}
            </label>
            <input
              id={`tk-pre-${f}`}
              class="tk-text"
              // All three are text boxes, but Preact's types cannot take a union here (tsc
              // fails without the assertion, which the linter's view of the types misses).
              // eslint-disable-next-line @typescript-eslint/no-unnecessary-type-assertion
              type={INPUT[f].type as "text"}
              autocomplete={INPUT[f].autocomplete}
              maxLength={LIMITS[f]}
              required={required}
              value={values[f]}
              disabled={busy}
              aria-invalid={errors[f] ? true : undefined}
              aria-describedby={errors[f] ? `tk-pre-${f}-error` : undefined}
              onInput={(e) => setValues({ ...values, [f]: e.currentTarget.value })}
            />
            {errors[f] ? (
              <span id={`tk-pre-${f}-error`} class="tk-field-error">
                {errors[f]}
              </span>
            ) : null}
          </div>
        );
      })}
      {offline ? (
        <div class="tk-field">
          <label for="tk-pre-message">
            {t("message")}
            <span aria-hidden="true"> *</span>
          </label>
          <textarea
            id="tk-pre-message"
            class="tk-text tk-textarea"
            rows={3}
            required
            value={message}
            disabled={busy}
            aria-invalid={errors.message ? true : undefined}
            aria-describedby={errors.message ? "tk-pre-message-error" : undefined}
            onInput={(e) => setMessage(e.currentTarget.value)}
          />
          {errors.message ? (
            <span id="tk-pre-message-error" class="tk-field-error">
              {errors.message}
            </span>
          ) : null}
        </div>
      ) : null}
      {form.consent ? (
        <div class="tk-field">
          <label class="tk-consent">
            <input
              id="tk-pre-consent"
              type="checkbox"
              checked={agreed}
              disabled={busy}
              required
              aria-invalid={errors.consent ? true : undefined}
              aria-describedby={errors.consent ? "tk-pre-consent-error" : undefined}
              onChange={(e) => setAgreed(e.currentTarget.checked)}
            />
            <span>
              {form.consent.text ?? t("consent")}.{" "}
              <a href={form.consent.policyUrl} target="_blank" rel="noopener noreferrer">
                {t("policy")}
              </a>
            </span>
          </label>
          {errors.consent ? (
            <span id="tk-pre-consent-error" class="tk-field-error">
              {errors.consent}
            </span>
          ) : null}
        </div>
      ) : null}
      {failed ? (
        <p class="tk-file-error" role="alert">
          {t("prechatFailed")}
        </p>
      ) : null}
      <button type="submit" class="tk-start" disabled={busy}>
        {t(offline ? "leave" : "start")}
      </button>
    </form>
  );
}
