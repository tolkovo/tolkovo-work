import { useEffect, useLayoutEffect, useRef, useState } from "preact/hooks";

import type { Controller, OutFile, State } from "../controller";
import { ACCEPT, MAX_FILE_SIZE, MAX_FILES, fileType, formatSize, isImage } from "../files";
import type { Translate } from "../i18n";
import type { ChatFile, ChatMessage, Locale } from "../types";

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
  const canAttach = s.status !== "unavailable";
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
      ) : null}
      <Composer c={c} view={view} draft={draft} disabled={!canAttach} />
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
