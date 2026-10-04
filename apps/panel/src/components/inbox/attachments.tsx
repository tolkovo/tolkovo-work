import { fetchAttachmentLink, type Attachment, type Message, type MessageReport } from "@tolkovo/api-client";
import { useApiClient, useAttachmentLink } from "@tolkovo/api-client/react";
import { cn } from "@tolkovo/ui";
import { Download, FileArchive, FileText, Paperclip, ShieldAlert, ShieldCheck, X } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";

/** Pictures the panel shows in the feed; every other file is a row with "Скачать". */
const PICTURES = new Set(["image/png", "image/jpeg"]);

const isPicture = (a: Attachment) => a.status === "ready" && PICTURES.has(a.content_type);

/**
 * The files of a message (docs/02 §11.6, docs/07 §4): screenshots as pictures that open larger,
 * logs and archives as rows to download. Files always download: the panel never opens a log as a
 * page. A rejected file stays in the list with the reason, so the operator knows it was sent.
 */
export function MessageAttachments({ message: m }: { message: Message }) {
  if (m.attachments.length === 0) return null;
  const checks = m.report?.files;
  const pictures = m.attachments.filter(isPicture);
  const files = m.attachments.map((a, i) => ({ a, check: checks?.[i] })).filter(({ a }) => !isPicture(a));
  return (
    <div className="mt-1.5 flex flex-col gap-1.5 whitespace-normal">
      {pictures.length > 0 && (
        <ul className="m-0 flex list-none flex-wrap gap-1.5 p-0">
          {pictures.map((a) => (
            <li key={a.id} className="min-w-0">
              <Picture attachment={a} />
            </li>
          ))}
        </ul>
      )}
      {files.length > 0 && (
        <ul className="m-0 flex list-none flex-col gap-1 p-0">
          {files.map(({ a, check }) => (
            <li key={a.id}>
              <FileRow attachment={a} check={check} fromReport={m.report !== undefined} />
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

function FileIcon({ contentType, className }: { contentType: string; className: string }) {
  if (contentType.startsWith("text/") || contentType.endsWith("/json") || contentType.endsWith("/xml")) {
    return <FileText aria-hidden="true" className={className} />;
  }
  if (contentType === "application/zip" || contentType === "application/gzip") {
    return <FileArchive aria-hidden="true" className={className} />;
  }
  return <Paperclip aria-hidden="true" className={className} />;
}

/** "38 КБ", "2,4 МБ" in the reader's language. */
export function formatSize(bytes: number, language: string): string {
  const [value, unit] =
    bytes < 1024 ? [bytes, "byte"] : bytes < 1024 * 1024 ? [bytes / 1024, "kilobyte"] : [bytes / (1024 * 1024), "megabyte"];
  return new Intl.NumberFormat(language, {
    style: "unit",
    unit,
    unitDisplay: "short",
    maximumFractionDigits: value < 10 && unit !== "byte" ? 1 : 0,
  }).format(value);
}

function FileRow({
  attachment: a,
  check,
  fromReport,
}: {
  attachment: Attachment;
  check: MessageReport["files"][number] | undefined;
  fromReport: boolean;
}) {
  const { t, i18n } = useTranslation();
  const download = useDownload(a);
  const state =
    a.status === "rejected"
      ? check && check !== "ok"
        ? t(`inbox.attachment.rejected.${check}`)
        : fromReport
          ? t("inbox.attachment.rejected.other")
          : t("inbox.attachmentRejected")
      : a.status === "expired"
        ? t("inbox.attachment.expired")
        : a.status !== "ready"
          ? t("inbox.attachment.pending")
          : null;
  return (
    <div className="flex flex-col gap-0.5">
      <div
        className={cn(
          "flex max-w-full items-center gap-2 rounded-md border border-line bg-surface px-2 py-1.5 text-[13px]",
          state && "border-dashed",
        )}
      >
        <FileIcon contentType={a.content_type} className={cn("size-4 shrink-0", state ? "text-ink-3" : "text-teal")} />
        <span className="flex min-w-0 flex-1 flex-col">
          <span className={cn("font-mono text-[12.5px] break-all", state ? "text-ink-3 line-through" : "text-ink")}>
            {a.file_name}
          </span>
          <span className={cn("text-[11.5px]", a.status === "rejected" ? "text-danger" : "text-ink-3")}>
            {state ?? formatSize(a.size, i18n.language)}
          </span>
        </span>
        {a.status === "ready" && (
          <button
            type="button"
            onClick={download.start}
            disabled={download.pending}
            aria-label={t("inbox.attachment.downloadName", { name: a.file_name })}
            className="flex h-7 shrink-0 items-center gap-1 rounded-md px-2 text-xs font-medium text-ink-2 hover:bg-hover hover:text-ink disabled:opacity-60"
          >
            <Download aria-hidden="true" className="size-3.5" />
            {t("inbox.attachment.download")}
          </button>
        )}
      </div>
      {download.failed && (
        <p role="alert" className="m-0 text-xs text-danger">
          {t("inbox.attachment.linkError")}
        </p>
      )}
    </div>
  );
}

/** Asks for a fresh signed link and follows it; the server answers with the file as a download. */
function useDownload(a: Attachment) {
  const api = useApiClient();
  const [pending, setPending] = useState(false);
  const [failed, setFailed] = useState(false);
  const start = () => {
    setPending(true);
    setFailed(false);
    fetchAttachmentLink(api, a.id).then(
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

function Picture({ attachment: a }: { attachment: Attachment }) {
  const { t } = useTranslation();
  const link = useAttachmentLink(a.id);
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

  if (link.isError || broken) {
    return <FileRow attachment={a} check={undefined} fromReport={false} />;
  }
  return (
    <>
      <button
        ref={opener}
        type="button"
        onClick={() => setOpen(true)}
        aria-label={t("inbox.attachment.open", { name: a.file_name })}
        className="block overflow-hidden rounded-md border border-line bg-paper hover:border-ink-3"
      >
        {link.data ? (
          <img src={link.data.url} alt="" onError={onError} className="block h-[140px] w-auto max-w-[260px] object-cover" />
        ) : (
          <span aria-hidden="true" className="block h-[140px] w-[200px] animate-pulse bg-hover" />
        )}
      </button>
      {open && link.data && (
        <Viewer
          url={link.data.url}
          attachment={a}
          onClose={() => {
            setOpen(false);
            opener.current?.focus();
          }}
        />
      )}
    </>
  );
}

/** A screenshot over the panel: Esc, the cross or a click beside it closes it. */
function Viewer({ url, attachment: a, onClose }: { url: string; attachment: Attachment; onClose: () => void }) {
  const { t } = useTranslation();
  const close = useRef<HTMLButtonElement>(null);
  const download = useDownload(a);
  useEffect(() => {
    close.current?.focus();
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.stopPropagation();
        onClose();
      }
    };
    document.addEventListener("keydown", onKey, true);
    return () => document.removeEventListener("keydown", onKey, true);
  }, [onClose]);
  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-label={t("inbox.attachment.viewer", { name: a.file_name })}
      className="fixed inset-0 z-50 flex flex-col gap-3 bg-ink/70 p-4"
      onClick={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <div className="flex items-center gap-2 rounded-lg bg-surface px-3 py-1.5 text-sm text-ink shadow-pop">
        <span className="min-w-0 flex-1 truncate font-mono">{a.file_name}</span>
        <button
          type="button"
          onClick={download.start}
          disabled={download.pending}
          className="flex h-8 items-center gap-1.5 rounded-md px-2.5 hover:bg-hover disabled:opacity-60"
        >
          <Download aria-hidden="true" className="size-4" />
          {t("inbox.attachment.download")}
        </button>
        <button
          ref={close}
          type="button"
          onClick={onClose}
          aria-label={t("inbox.attachment.close")}
          className="grid size-8 place-items-center rounded-md hover:bg-hover"
        >
          <X aria-hidden="true" className="size-5" />
        </button>
      </div>
      <div
        className="flex min-h-0 flex-1 items-center justify-center"
        onClick={(e) => {
          if (e.target === e.currentTarget) onClose();
        }}
      >
        <img src={url} alt={a.file_name} className="max-h-full max-w-full rounded-md bg-surface object-contain" />
      </div>
      {download.failed && (
        <p role="alert" className="m-0 self-center rounded-md bg-surface px-3 py-1.5 text-sm text-danger">
          {t("inbox.attachment.linkError")}
        </p>
      )}
    </div>
  );
}

/**
 * What the app said about itself, above the report text (docs/02 §11.6): name and version, OS,
 * language, more facts on request, and whether the company's backend vouched for the user.
 */
export function ReportDetails({ report: r }: { report: MessageReport }) {
  const { t } = useTranslation();
  const app = [r.app.name, r.app.version].filter(Boolean).join(" ");
  const os = [r.app.os, r.app.os_version].filter(Boolean).join(" ");
  const extra = Object.entries(r.app.extra ?? {});
  return (
    <div className="mb-1.5 flex flex-col gap-1 border-b border-line pb-1.5 text-xs whitespace-normal text-ink-2">
      <p className="m-0 flex flex-wrap items-center gap-x-2 gap-y-0.5">
        <span className="font-semibold tracking-[0.04em] text-ink-3 uppercase">{t("inbox.report.title")}</span>
        {app && <span className="font-mono text-[12px] text-ink">{app}</span>}
        {os && <span className="font-mono text-[12px]">{os}</span>}
        {r.app.locale && <span className="font-mono text-[12px]">{r.app.locale}</span>}
      </p>
      {r.contact_verified ? (
        <p className="m-0 flex items-center gap-1 text-teal">
          <ShieldCheck aria-hidden="true" className="size-3.5 shrink-0" />
          {t("inbox.report.verified")}
        </p>
      ) : (
        <p className="m-0 flex items-start gap-1 text-lamp-ink" title={t("inbox.report.unverifiedHint")}>
          <ShieldAlert aria-hidden="true" className="mt-px size-3.5 shrink-0" />
          <span>
            {t("inbox.report.unverified")}
            <span className="sr-only">. {t("inbox.report.unverifiedHint")}</span>
          </span>
        </p>
      )}
      {extra.length > 0 && (
        <details>
          <summary className="cursor-pointer text-ink-3 hover:text-ink">{t("inbox.report.more", { count: extra.length })}</summary>
          <dl className="m-0 mt-1 grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-0.5">
            {extra.map(([k, v]) => (
              <div key={k} className="contents">
                <dt className="font-mono text-ink-3">{k}</dt>
                <dd className="m-0 font-mono break-all text-ink">{v}</dd>
              </div>
            ))}
          </dl>
        </details>
      )}
    </div>
  );
}
