import { escalateConversation, type Conversation, type Line, type MessageEvent } from "@tolkovo/api-client";
import { useLines } from "@tolkovo/api-client/react";
import { Button, Select, cn } from "@tolkovo/ui";
import { useNavigate } from "@tanstack/react-router";
import { ArrowUpRight } from "lucide-react";
import { useId, useState, type FormEvent, type ReactNode } from "react";
import { useTranslation } from "react-i18next";

import { describeApiError } from "../../lib/api-errors";
import { useAbilities, type ConversationChange } from "../../lib/inbox/actions";
import { inboxSearch, type InboxScope, type InboxTab } from "../../lib/inbox/tabs";
import { FormMessage } from "../form/fields";

/**
 * Lines of support L1/L2/L3 (ADR-0021). A conversation without a line is on the first line; an
 * operator moves it up to specialists with a reason, or back. The inbox shows the line of those
 * above the first and filters by it.
 */

/** The lines of the workspace by id. */
export function useLineMap(): Map<string, Line> {
  const { data } = useLines();
  return new Map((data ?? []).map((line) => [line.id, line]));
}

/** «L2 · Биллинг»: the level first, so the row tells at a glance how far it went. */
export function LinePill({ line, className }: { line: Line; className?: string }) {
  const { t } = useTranslation();
  return (
    <span
      className={cn(
        "inline-flex h-[18px] shrink-0 items-center gap-1 rounded-sm bg-sunk px-1.5 text-[10.5px] font-medium text-ink",
        className,
      )}
    >
      <span className="font-mono">{t("inbox.line.level", { level: line.level })}</span>
      <span className="text-ink-2">{line.name}</span>
    </span>
  );
}

/** The name of a line in sentences; the first line when there is none. */
function useLineName() {
  const { t } = useTranslation();
  const lines = useLineMap();
  return (id: string | null | undefined) => {
    if (!id) return t("inbox.line.first");
    const line = lines.get(id);
    return line ? t("inbox.line.named", { level: line.level, name: line.name }) : t("inbox.line.deleted");
  };
}

/** The feed line of a move: «Передан на линию L2 «Биллинг» · Анна», «Возвращён на первую линию · Олег». */
export function LineEventText({ event: e, by }: { event: MessageEvent; by: string }) {
  const { t } = useTranslation();
  const lines = useLineMap();
  const name = useLineName();
  const level = (id: string | null | undefined) => (id ? (lines.get(id)?.level ?? 1) : 1);
  if (!e.line_id) return <>{t("inbox.event.lineFirst") + by}</>;
  const down = level(e.line_id) < level(e.previous_line_id);
  return <>{t(down ? "inbox.event.lineDown" : "inbox.event.lineUp", { line: name(e.line_id) }) + by}</>;
}

/**
 * The line of the conversation in the bar under its header and, for who may move it, the form to
 * pass it to another line: up needs a reason, which becomes a note for the specialist.
 */
export function ConversationLine({ conversation: c, change }: { conversation: Conversation; change: ConversationChange }) {
  const { t } = useTranslation();
  const can = useAbilities(c);
  const { data: lines } = useLines();
  const name = useLineName();
  const [open, setOpen] = useState(false);
  if (!lines || (lines.length === 0 && !c.line_id)) return null;
  const current = c.line_id ? lines.find((l) => l.id === c.line_id) : undefined;
  return (
    <>
      {current ? (
        <LinePill line={current} />
      ) : (
        c.line_id && <span className="text-xs text-ink-2">{name(c.line_id)}</span>
      )}
      {can.escalate && !open && (
        <button
          type="button"
          onClick={() => setOpen(true)}
          className="inline-flex h-[22px] items-center gap-1 rounded-full border border-line px-2 text-xs text-ink-2 hover:bg-hover hover:text-ink"
        >
          <ArrowUpRight aria-hidden="true" className="size-3.5" />
          {t("inbox.line.move")}
        </button>
      )}
      {can.escalate && open && <EscalateForm conversation={c} lines={lines} change={change} onDone={() => setOpen(false)} />}
    </>
  );
}

function EscalateForm({
  conversation: c,
  lines,
  change,
  onDone,
}: {
  conversation: Conversation;
  lines: Line[];
  change: ConversationChange;
  onDone: () => void;
}) {
  const { t } = useTranslation();
  const id = useId();
  const targets = lines.filter((l) => l.id !== c.line_id);
  const [target, setTarget] = useState(targets.find((l) => l.level > c.line_level)?.id ?? targets[0]?.id ?? "");
  const [reason, setReason] = useState("");
  const [error, setError] = useState<string | null>(null);
  const level = target === "" ? 1 : (lines.find((l) => l.id === target)?.level ?? 1);
  const up = level > c.line_level;

  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (up && !reason.trim()) {
      setError(t("inbox.line.reasonRequired"));
      document.getElementById(`${id}-reason`)?.focus();
      return;
    }
    setError(null);
    change.mutate((api) => escalateConversation(api, c.id, target || null, reason.trim(), c.version), {
      onSuccess: onDone,
      onError: (err) => setError(describeApiError(t, err).message),
    });
  };

  return (
    <form
      noValidate
      onSubmit={submit}
      aria-label={t("inbox.line.move")}
      className="flex w-full flex-col gap-2 rounded-lg border border-line bg-paper p-2.5"
    >
      <div className="flex flex-wrap items-center gap-2">
        <label htmlFor={`${id}-line`} className="text-xs text-ink-2">
          {t("inbox.line.to")}
        </label>
        <div className="w-56 max-w-full">
          <Select id={`${id}-line`} value={target} onChange={(e) => setTarget(e.currentTarget.value)}>
            {targets.map((l) => (
              <option key={l.id} value={l.id}>
                {t("inbox.line.named", { level: l.level, name: l.name })}
              </option>
            ))}
            {c.line_id !== null && <option value="">{t("inbox.line.first")}</option>}
          </Select>
        </div>
      </div>
      <label htmlFor={`${id}-reason`} className="text-xs text-ink-2">
        {t(up ? "inbox.line.reason" : "inbox.line.comment")}
      </label>
      <textarea
        id={`${id}-reason`}
        value={reason}
        maxLength={1000}
        rows={2}
        aria-describedby={`${id}-hint`}
        aria-invalid={error !== null && up && !reason.trim() ? true : undefined}
        onChange={(e) => setReason(e.currentTarget.value)}
        className="w-full resize-y rounded-md border border-line bg-surface px-2 py-1.5 text-[13px] text-ink"
      />
      <p id={`${id}-hint`} className="m-0 text-[12px] text-ink-3">
        {t("inbox.line.hint")}
      </p>
      <FormMessage tone="error" message={error} />
      <div className="flex gap-2">
        <Button type="submit" size="sm" variant="primary" disabled={change.isPending || targets.length === 0}>
          {t("inbox.line.submit")}
        </Button>
        <Button type="button" size="sm" onClick={onDone}>
          {t("tasks.cancel")}
        </Button>
      </div>
    </form>
  );
}

/**
 * Chips over the list: all lines, the first line, each line. The choice lives in `?line=`, next
 * to the topic.
 */
export function LineFilter({ tab, scope }: { tab: InboxTab; scope: InboxScope }) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { data: lines } = useLines();
  if (!lines || lines.length === 0) return null;
  const chip = (value: string | undefined, label: ReactNode, key: string) => (
    <button
      key={key}
      type="button"
      aria-pressed={scope.line === value}
      onClick={() => void navigate({ to: "/inbox", search: inboxSearch(tab, { ...scope, line: value }) })}
      className={cn(
        "inline-flex h-6 shrink-0 items-center gap-1 rounded-full border border-line bg-transparent px-2 text-xs whitespace-nowrap text-ink-2",
        "hover:bg-hover hover:text-ink aria-pressed:border-ink aria-pressed:bg-sel aria-pressed:text-ink",
      )}
    >
      {label}
    </button>
  );
  return (
    <div role="group" aria-label={t("inbox.line.filter")} className="flex gap-1.5 overflow-x-auto border-b border-line px-3 py-2">
      {chip(undefined, t("inbox.line.all"), "all")}
      {chip("first", t("inbox.line.first"), "first")}
      {lines.map((l) =>
        chip(
          l.id,
          <>
            <span className="font-mono text-ink">{t("inbox.line.level", { level: l.level })}</span>
            {l.name}
          </>,
          l.id,
        ),
      )}
    </div>
  );
}
