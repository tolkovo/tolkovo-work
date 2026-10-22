import { Button } from "@tolkovo/ui";
import { Lock } from "lucide-react";
import { useEffect, useId, useRef, useState } from "react";
import { useTranslation } from "react-i18next";

import { control } from "./task-bits";

/** The longest reason the server keeps (ADR-0019, K2). */
const MAX_REASON = 500;

/**
 * Asks what blocks a task. The mark goes over the status, so nothing else changes; Escape and a
 * click outside cancel.
 */
export function BlockDialog({ number, onSubmit, onCancel }: { number: string; onSubmit: (reason: string) => void; onCancel: () => void }) {
  const { t } = useTranslation();
  const titleId = useId();
  const fieldId = useId();
  const hintId = useId();
  const field = useRef<HTMLTextAreaElement>(null);
  const [reason, setReason] = useState("");
  useEffect(() => field.current?.focus(), []);
  const text = reason.trim();
  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-labelledby={titleId}
      className="fixed inset-0 z-50 grid place-items-center bg-ink/35 p-4"
      onKeyDown={(e) => {
        if (e.key === "Escape") {
          e.preventDefault();
          onCancel();
        }
      }}
      onClick={(e) => {
        if (e.target === e.currentTarget) onCancel();
      }}
    >
      <form
        className="flex w-full max-w-105 flex-col gap-3 rounded-2xl border border-line bg-surface p-4.5 text-ink shadow-pop"
        onSubmit={(e) => {
          e.preventDefault();
          if (text) onSubmit(text);
        }}
      >
        <h2 id={titleId} className="m-0 text-[15.5px] font-semibold">
          {t("tasks.blocked.title", { number })}
        </h2>
        <label htmlFor={fieldId} className="text-[13px] font-medium">
          {t("tasks.blocked.reason")}
        </label>
        <textarea
          id={fieldId}
          ref={field}
          rows={3}
          required
          maxLength={MAX_REASON}
          value={reason}
          aria-describedby={hintId}
          placeholder={t("tasks.blocked.reasonPlaceholder")}
          onChange={(e) => setReason(e.currentTarget.value)}
          className={`${control} py-2`}
        />
        <p id={hintId} className="m-0 text-[12.5px] text-ink-3">
          {t("tasks.blocked.hint")}
        </p>
        <div className="flex justify-end gap-2">
          <Button type="button" size="sm" onClick={onCancel}>
            {t("tasks.blocked.cancel")}
          </Button>
          <Button type="submit" size="sm" variant="primary" disabled={!text}>
            {t("tasks.blocked.submit")}
          </Button>
        </div>
      </form>
    </div>
  );
}

/** The mark on the open task: the reason and a way to take it off. */
export function BlockedNote({ reason, disabled, onUnblock }: { reason: string; disabled: boolean; onUnblock: () => void }) {
  const { t } = useTranslation();
  return (
    <div role="note" className="flex items-start gap-2.5 rounded-lg bg-lamp-soft px-3 py-2.5 text-[13px] text-lamp-ink">
      <Lock aria-hidden="true" className="mt-0.5 size-4 shrink-0" />
      <p className="m-0 min-w-0 flex-1 break-words">
        <b>{t("tasks.blocked.banner")}.</b> {reason}
      </p>
      <Button size="sm" variant="ghost" disabled={disabled} onClick={onUnblock} className="-my-1 text-lamp-ink">
        {t("tasks.blocked.unblock")}
      </Button>
    </div>
  );
}
