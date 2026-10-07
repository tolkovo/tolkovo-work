import { fetchTaskFileLink, uploadTaskFile, type TaskFile } from "@tolkovo/api-client";
import { useApiClient, useTaskFileLink } from "@tolkovo/api-client/react";
import { cn } from "@tolkovo/ui";
import { Download, Loader2, Paperclip, X } from "lucide-react";
import { useCallback, useRef, useState, type ClipboardEvent, type DragEvent, type ReactNode } from "react";
import { useTranslation } from "react-i18next";

import { FileIcon, ImageViewer, formatSize } from "../inbox/attachments";

/**
 * Files of tasks (ADR-0015): screenshots, logs and documents in the description and in comments.
 * A file is uploaded as soon as it is chosen, pasted (Ctrl+V) or dropped, and is sent with the
 * task or the comment; the server checks it then.
 */

/** What the server takes, as in conversations: 25 MB, ten at once, these types. */
const MAX_SIZE = 25 * 1024 * 1024;
const MAX_AT_ONCE = 10;
const TYPES = [
  "image/png",
  "image/jpeg",
  "image/gif",
  "image/webp",
  "application/pdf",
  "text/plain",
  "text/csv",
  "application/json",
  "application/zip",
  "application/gzip",
  "application/msword",
  "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
  "application/vnd.ms-excel",
  "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
];
/** Browsers give .log and .md files no type or an odd one; they are text. */
const TEXT_EXT = /\.(log|txt|md|ini|conf|yaml|yml|trace)$/i;
const PICTURES = new Set(["image/png", "image/jpeg", "image/gif", "image/webp"]);

type Draft = { key: string; name: string; size: number; state: "uploading" | "ready" | "failed"; id?: string; error?: string };

/** The type the server is told: the browser's, or text for a log it does not know. */
function fileType(f: File): string {
  if (TYPES.includes(f.type)) return f.type;
  if ((f.type === "" || f.type === "application/octet-stream") && TEXT_EXT.test(f.name)) return "text/plain";
  return f.type;
}

/** Files being uploaded for a task or a comment, before it is sent. */
export function useFileDrafts() {
  const { t } = useTranslation();
  const client = useApiClient();
  const [drafts, setDrafts] = useState<Draft[]>([]);
  const [notice, setNotice] = useState<string | null>(null);
  const seq = useRef(0);

  const add = (files: File[]) => {
    setNotice(null);
    const room = MAX_AT_ONCE - drafts.length;
    if (files.length > room) setNotice(t("tasks.files.tooMany", { count: MAX_AT_ONCE }));
    for (const raw of files.slice(0, Math.max(room, 0))) {
      const key = `f${++seq.current}`;
      const type = fileType(raw);
      // A screenshot from the clipboard is "image.png" in every browser; give it a better name.
      const name = raw.name && raw.name !== "image.png" ? raw.name : `screenshot-${new Date().toISOString().slice(0, 19).replace(/[:T]/g, "-")}.png`;
      if (!TYPES.includes(type)) {
        setDrafts((d) => [...d, { key, name, size: raw.size, state: "failed", error: t("tasks.files.badType") }]);
        continue;
      }
      if (raw.size > MAX_SIZE || raw.size === 0) {
        setDrafts((d) => [...d, { key, name, size: raw.size, state: "failed", error: t("tasks.files.tooBig") }]);
        continue;
      }
      setDrafts((d) => [...d, { key, name, size: raw.size, state: "uploading" }]);
      const file = new File([raw], name, { type });
      uploadTaskFile(client, file).then(
        (f) => setDrafts((d) => d.map((x) => (x.key === key ? { ...x, state: "ready", id: f.id } : x))),
        () => setDrafts((d) => d.map((x) => (x.key === key ? { ...x, state: "failed", error: t("tasks.files.failed") } : x))),
      );
    }
  };

  /** Takes sent files out of the list; refused ones stay, with their reason. */
  const drop = useCallback((ids: string[]) => {
    setDrafts((d) => d.filter((x) => !x.id || !ids.includes(x.id)));
    setNotice(null);
  }, []);

  return {
    drafts,
    notice,
    add,
    remove: (key: string) => setDrafts((d) => d.filter((x) => x.key !== key)),
    drop,
    ids: drafts.filter((d) => d.state === "ready" && d.id).map((d) => d.id as string),
    uploading: drafts.some((d) => d.state === "uploading"),
  };
}

export type FileDrafts = ReturnType<typeof useFileDrafts>;

/** Files of an event, pasted or dropped. */
function filesOf(list: DataTransferItemList | FileList | null | undefined): File[] {
  if (!list) return [];
  const out: File[] = [];
  for (const item of Array.from(list as ArrayLike<DataTransferItem | File>)) {
    if (item instanceof File) out.push(item);
    else if (item.kind === "file") {
      const f = item.getAsFile();
      if (f) out.push(f);
    }
  }
  return out;
}

/**
 * A field that takes files: paste a screenshot, drop files on it, or choose them with the
 * button. The chosen files show below with their upload state.
 */
export function AttachArea({ drafts, children, label }: { drafts: FileDrafts; children?: ReactNode; label?: string }) {
  const { t } = useTranslation();
  const input = useRef<HTMLInputElement>(null);
  const [over, setOver] = useState(false);

  const onPaste = (e: ClipboardEvent) => {
    const files = filesOf(e.clipboardData.items);
    if (files.length === 0) return; // plain text pastes as usual
    e.preventDefault();
    drafts.add(files);
  };
  const onDrop = (e: DragEvent) => {
    e.preventDefault();
    setOver(false);
    drafts.add(filesOf(e.dataTransfer.files));
  };

  return (
    <div
      onPaste={onPaste}
      onDragOver={(e) => {
        if (Array.from(e.dataTransfer.types).includes("Files")) {
          e.preventDefault();
          setOver(true);
        }
      }}
      onDragLeave={() => setOver(false)}
      onDrop={onDrop}
      className={cn("flex flex-col gap-2 rounded-lg", over && "outline-2 outline-offset-4 outline-teal outline-dashed")}
    >
      {children}
      <div className="flex flex-wrap items-center gap-2">
        <button
          type="button"
          onClick={() => input.current?.click()}
          className="flex h-8 items-center gap-1.5 rounded-md border border-line bg-surface px-2.5 text-[13px] font-medium text-ink-2 hover:bg-hover hover:text-ink"
        >
          <Paperclip aria-hidden="true" className="size-4" />
          {label ?? t("tasks.files.attach")}
        </button>
        <span className="text-[12px] text-ink-3">{t("tasks.files.hint")}</span>
        <input
          ref={input}
          type="file"
          multiple
          hidden
          accept={[...TYPES, ".log", ".txt", ".md"].join(",")}
          onChange={(e) => {
            drafts.add(filesOf(e.currentTarget.files));
            e.currentTarget.value = "";
          }}
        />
      </div>
      {drafts.notice && (
        <p role="alert" className="m-0 text-[12.5px] text-danger">
          {drafts.notice}
        </p>
      )}
      {drafts.drafts.length > 0 && (
        <ul aria-label={t("tasks.files.chosen")} className="m-0 flex list-none flex-wrap gap-1.5 p-0">
          {drafts.drafts.map((d) => (
            <li
              key={d.key}
              className={cn(
                "flex max-w-full items-center gap-1.5 rounded-md border bg-surface py-1 pr-1 pl-2 text-[12.5px]",
                d.state === "failed" ? "border-danger" : "border-line",
              )}
            >
              {d.state === "uploading" ? (
                <Loader2 aria-hidden="true" className="size-3.5 shrink-0 animate-spin text-ink-3" />
              ) : (
                <Paperclip aria-hidden="true" className="size-3.5 shrink-0 text-ink-3" />
              )}
              <span className="min-w-0 truncate font-mono">{d.name}</span>
              <span className={cn("shrink-0", d.state === "failed" ? "text-danger" : "text-ink-3")}>
                {d.state === "failed" ? d.error : d.state === "uploading" ? t("tasks.files.uploading") : null}
              </span>
              <button
                type="button"
                onClick={() => drafts.remove(d.key)}
                aria-label={t("tasks.files.unchoose", { name: d.name })}
                className="grid size-6 shrink-0 place-items-center rounded text-ink-3 hover:bg-hover hover:text-ink"
              >
                <X aria-hidden="true" className="size-3.5" />
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

/** Files of the description or of a comment: pictures to open, other files to download. */
export function TaskFileList({ files, onRemove, removing }: { files: TaskFile[]; onRemove?: (f: TaskFile) => void; removing?: boolean }) {
  if (files.length === 0) return null;
  const pictures = files.filter((f) => PICTURES.has(f.content_type));
  const others = files.filter((f) => !PICTURES.has(f.content_type));
  return (
    <div className="flex flex-col gap-1.5">
      {pictures.length > 0 && (
        <ul className="m-0 flex list-none flex-wrap gap-1.5 p-0">
          {pictures.map((f) => (
            <li key={f.id} className="relative min-w-0">
              <Picture file={f} />
              {onRemove && <RemoveButton file={f} onRemove={onRemove} disabled={removing} overlay />}
            </li>
          ))}
        </ul>
      )}
      {others.length > 0 && (
        <ul className="m-0 flex list-none flex-col gap-1 p-0">
          {others.map((f) => (
            <li key={f.id} className="flex items-center gap-1">
              <FileRow file={f} />
              {onRemove && <RemoveButton file={f} onRemove={onRemove} disabled={removing} />}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

function RemoveButton({ file, onRemove, disabled, overlay }: { file: TaskFile; onRemove: (f: TaskFile) => void; disabled?: boolean | undefined; overlay?: boolean }) {
  const { t } = useTranslation();
  return (
    <button
      type="button"
      disabled={disabled}
      onClick={() => onRemove(file)}
      aria-label={t("tasks.files.remove", { name: file.file_name })}
      title={t("tasks.files.remove", { name: file.file_name })}
      className={cn(
        "grid size-7 shrink-0 place-items-center rounded-md text-ink-3 hover:bg-hover hover:text-ink disabled:opacity-60",
        overlay && "absolute top-1 right-1 bg-surface/90 shadow-pop",
      )}
    >
      <X aria-hidden="true" className="size-4" />
    </button>
  );
}

/** Asks for a fresh signed link and follows it; the server answers with the file as a download. */
function useDownload(f: TaskFile) {
  const api = useApiClient();
  const [pending, setPending] = useState(false);
  const [failed, setFailed] = useState(false);
  const start = () => {
    setPending(true);
    setFailed(false);
    fetchTaskFileLink(api, f.id).then(
      (link) => {
        setPending(false);
        const el = document.createElement("a");
        el.href = link.url;
        el.rel = "noopener";
        document.body.append(el);
        el.click();
        el.remove();
      },
      () => {
        setPending(false);
        setFailed(true);
      },
    );
  };
  return { start, pending, failed };
}

function FileRow({ file: f }: { file: TaskFile }) {
  const { t, i18n } = useTranslation();
  const download = useDownload(f);
  return (
    <div className="flex min-w-0 flex-1 flex-col gap-0.5">
      <div className="flex max-w-full items-center gap-2 rounded-md border border-line bg-surface px-2 py-1.5 text-[13px]">
        <FileIcon contentType={f.content_type} className="size-4 shrink-0 text-teal" />
        <span className="flex min-w-0 flex-1 flex-col">
          <span className="font-mono text-[12.5px] break-all text-ink">{f.file_name}</span>
          <span className="text-[11.5px] text-ink-3">{formatSize(f.size, i18n.language)}</span>
        </span>
        <button
          type="button"
          onClick={download.start}
          disabled={download.pending}
          aria-label={t("inbox.attachment.downloadName", { name: f.file_name })}
          className="flex h-7 shrink-0 items-center gap-1 rounded-md px-2 text-xs font-medium text-ink-2 hover:bg-hover hover:text-ink disabled:opacity-60"
        >
          <Download aria-hidden="true" className="size-3.5" />
          {t("inbox.attachment.download")}
        </button>
      </div>
      {download.failed && (
        <p role="alert" className="m-0 text-xs text-danger">
          {t("inbox.attachment.linkError")}
        </p>
      )}
    </div>
  );
}

function Picture({ file: f }: { file: TaskFile }) {
  const { t } = useTranslation();
  const link = useTaskFileLink(f.id);
  const download = useDownload(f);
  const [broken, setBroken] = useState(false);
  const [open, setOpen] = useState(false);
  const opener = useRef<HTMLButtonElement>(null);
  // A link older than its 15 minutes fails once: ask for a new one, then give up.
  const retried = useRef(false);
  const onError = () => {
    if (retried.current) return setBroken(true);
    retried.current = true;
    void link.refetch();
  };
  if (link.isError || broken) return <FileRow file={f} />;
  return (
    <>
      <button
        ref={opener}
        type="button"
        onClick={() => setOpen(true)}
        aria-label={t("inbox.attachment.open", { name: f.file_name })}
        className="block overflow-hidden rounded-md border border-line bg-paper hover:border-ink-3"
      >
        {link.data ? (
          <img src={link.data.url} alt="" onError={onError} className="block h-[140px] w-auto max-w-[260px] object-cover" />
        ) : (
          <span aria-hidden="true" className="block h-[140px] w-[200px] animate-pulse bg-hover" />
        )}
      </button>
      {open && link.data && (
        <ImageViewer
          url={link.data.url}
          name={f.file_name}
          download={download}
          onClose={() => {
            setOpen(false);
            opener.current?.focus();
          }}
        />
      )}
    </>
  );
}
