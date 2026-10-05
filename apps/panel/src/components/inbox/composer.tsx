import {
  assignConversation,
  changeConversationStatus,
  hasPermission,
  isApiError,
  type Conversation,
  type Message,
} from "@tolkovo/api-client";
import { useMessages, useSession } from "@tolkovo/api-client/react";
import { Button, cn } from "@tolkovo/ui";
import { ChevronDown, Lock } from "lucide-react";
import { useEffect, useId, useState, type KeyboardEvent } from "react";
import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";

import { useAbilities, useConversationChange, useSendMessage } from "../../lib/inbox/actions";
import { clearDraftText, updateDraft, useDraft, type ComposerMode } from "../../lib/inbox/drafts";
import { signalSender } from "../../lib/realtime/signals";
import { Menu } from "../menu";
import { actionError } from "./conversation-actions";
import { FixReplyOffer } from "./issues";
import { useMemberNames } from "./presence";

/** Characters a message may have (messages.body in api/openapi.yaml). */
export const MAX_BODY = 10_000;
/** The counter appears when this share of the limit is used. */
const COUNTER_FROM = 0.9;

type ComposerProps = {
  conversation: Conversation;
  /** Scrolls the feed to the newest message ("Посмотреть" of the collision guard). */
  onShowLatest: () => void;
};

/**
 * The composer (docs/07 §4, docs/04 §3.1): reply or internal note, the same text across the
 * switch. Enter sends, Shift+Enter breaks the line, Ctrl+Alt+N switches the mode, Ctrl+Alt+Enter
 * sends and closes. A new message from someone else while the operator typed pauses the send.
 */
export function Composer({ conversation: c, onShowLatest }: ComposerProps) {
  const { t } = useTranslation();
  const can = useAbilities(c);
  const draft = useDraft(c.id);
  const send = useSendMessage(c.id);
  const change = useConversationChange(c.id);
  const newestForeign = useNewestForeignId(c.id);
  const [paused, setPaused] = useState(false);
  const fieldId = useId();
  const hintId = useId();

  // Members who never reply start in the note; in someone else's conversation the reply tab
  // stays and explains why it is closed (docs/04 §3.1).
  const { data: session } = useSession();
  const replies = hasPermission(session, "conversations.reply");
  const mode: ComposerMode = !replies && can.note && draft.mode === "reply" ? "note" : draft.mode;
  const note = mode === "note";
  const allowed = note ? can.note : can.reply;

  // Leaving the conversation (or the composer) ends the typing indicator colleagues see.
  useEffect(() => () => signalSender.stopTyping(c.id), [c.id]);

  const setMode = (next: ComposerMode) => {
    if (next === "note") signalSender.stopTyping(c.id);
    updateDraft(c.id, { mode: next });
    setPaused(false);
  };

  const onChange = (text: string) => {
    // The first keystroke remembers what the operator had seen; an edit invalidates the key.
    updateDraft(c.id, { text, key: null, seenId: draft.text ? draft.seenId : newestForeign });
    setPaused(false);
    if (note) return;
    if (text.trim()) signalSender.typing(c.id);
    else signalSender.stopTyping(c.id);
  };

  const submit = (opts: { force?: boolean; andClose?: boolean } = {}) => {
    const body = draft.text.trim();
    if (!body || !allowed || send.isPending || [...body].length > MAX_BODY) return;
    if (!opts.force && newestForeign !== null && newestForeign !== draft.seenId) {
      setPaused(true);
      return;
    }
    setPaused(false);
    const key = draft.key ?? crypto.randomUUID();
    updateDraft(c.id, { key });
    signalSender.stopTyping(c.id);
    send.mutate(
      { body, private: note, idempotencyKey: key },
      {
        onSuccess: () => {
          clearDraftText(c.id);
          if (opts.andClose) change.mutate((api) => changeConversationStatus(api, c.id, "resolved"));
        },
      },
    );
  };

  const onKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.nativeEvent.isComposing) return;
    if (e.key.toLowerCase() === "n" && e.ctrlKey && e.altKey) {
      e.preventDefault();
      setMode(note ? "reply" : "note");
    } else if (e.key === "Enter" && e.ctrlKey && e.altKey) {
      e.preventDefault();
      if (!note && can.close) submit({ andClose: true });
    } else if (e.key === "Enter" && !e.shiftKey && !e.ctrlKey && !e.altKey && !e.metaKey) {
      e.preventDefault();
      submit();
    }
  };

  const length = [...draft.text].length;
  const tooLong = length > MAX_BODY;

  return (
    <div
      className={cn(
        "border-t px-4 pt-2 pb-3 max-nav:px-3",
        note ? "border-note-line bg-note" : "border-line bg-surface",
      )}
    >
      <div className="mb-1.5 flex items-center gap-3">
        <div role="group" aria-label={t("inbox.composer.mode")} className="flex gap-0.5 rounded-md bg-sunk p-0.5">
          {(["reply", "note"] as const).map((m) => (
            <button
              key={m}
              type="button"
              aria-pressed={mode === m}
              title={m === "note" ? "Ctrl+Alt+N" : undefined}
              onClick={() => setMode(m)}
              className="rounded-[5px] px-2.5 py-1 text-[12.5px] text-ink-2 aria-pressed:bg-surface aria-pressed:font-medium aria-pressed:text-ink aria-pressed:shadow-sm"
            >
              {t(`inbox.composer.${m}`)}
            </button>
          ))}
        </div>
        {note && (
          <span className="flex items-center gap-1 text-xs font-medium text-lamp-ink">
            <Lock aria-hidden="true" className="size-3.5" />
            {t("inbox.composer.noteHint")}
          </span>
        )}
      </div>

      {allowed ? (
        <>
          {!note && (
            <FixReplyOffer
              conversation={c}
              text={draft.text}
              onInsert={(text) => {
                updateDraft(c.id, { text, key: null, seenId: newestForeign });
                setPaused(false);
                document.getElementById(fieldId)?.focus();
              }}
            />
          )}
          <label htmlFor={fieldId} className="sr-only">
            {t(note ? "inbox.composer.noteLabel" : "inbox.composer.replyLabel")}
          </label>
          <textarea
            id={fieldId}
            value={draft.text}
            readOnly={send.isPending}
            aria-describedby={hintId}
            aria-invalid={tooLong || undefined}
            placeholder={t(note ? "inbox.composer.notePlaceholder" : "inbox.composer.replyPlaceholder")}
            onChange={(e) => onChange(e.target.value)}
            onKeyDown={onKeyDown}
            onBlur={() => signalSender.stopTyping(c.id)}
            rows={2}
            className={cn(
              "block max-h-48 min-h-14 w-full resize-none rounded-md border px-3 py-2 text-sm [field-sizing:content] focus-visible:outline-2 focus-visible:outline-teal",
              note ? "border-note-line bg-surface" : "border-line bg-paper",
            )}
          />
          {paused && (
            <div role="alert" className="mt-2 flex flex-wrap items-center gap-2 rounded-md bg-lamp-soft px-3 py-2 text-[13px] text-lamp-ink">
              <span className="mr-auto">{t("inbox.composer.collision")}</span>
              <Button
                size="sm"
                variant="ghost"
                onClick={() => {
                  setPaused(false);
                  updateDraft(c.id, { seenId: newestForeign });
                  onShowLatest();
                }}
              >
                {t("inbox.composer.showLatest")}
              </Button>
              <Button size="sm" variant="primary" onClick={() => submit({ force: true })}>
                {t("inbox.composer.sendAnyway")}
              </Button>
            </div>
          )}
          {send.isError && !send.isPending && (
            <p role="alert" className="m-0 mt-2 text-[13px] text-danger">
              {sendError(send.error, t)}
            </p>
          )}
          {change.isError && (
            <p role="alert" className="m-0 mt-2 text-[13px] text-danger">
              {t("inbox.composer.sentNotClosed")} {actionError(change.error, t)}
            </p>
          )}
          <div className="mt-2 flex items-center gap-3">
            <p id={hintId} className="m-0 mr-auto text-xs text-ink-3 max-nav:hidden">
              {t("inbox.composer.keys")}
            </p>
            {length >= MAX_BODY * COUNTER_FROM && (
              <span className={cn("font-mono text-xs tabular-nums", tooLong ? "text-danger" : "text-ink-3")}>
                {t("inbox.composer.counter", { count: length, max: MAX_BODY })}
              </span>
            )}
            <div className="ml-auto flex">
              <Button
                size="sm"
                variant="primary"
                disabled={!draft.text.trim() || tooLong || send.isPending}
                onClick={() => submit()}
                className={cn(!note && can.close && "rounded-r-none")}
              >
                {t(send.isPending ? "inbox.composer.sending" : note ? "inbox.composer.saveNote" : "inbox.composer.send")}
              </Button>
              {!note && can.close && (
                <Menu
                  label={t("inbox.composer.moreSend")}
                  trigger={<ChevronDown aria-hidden="true" className="size-4" />}
                  disabled={!draft.text.trim() || tooLong || send.isPending}
                  triggerClassName="grid h-[26px] w-7 place-items-center rounded-r-md border-l border-primary-foreground/30 bg-primary text-primary-foreground disabled:opacity-55"
                  items={[
                    {
                      label: t("inbox.composer.sendAndClose"),
                      hint: "Ctrl+Alt+Enter",
                      onSelect: () => submit({ andClose: true }),
                    },
                  ]}
                />
              )}
            </div>
          </div>
        </>
      ) : (
        <ReplyBlocked conversation={c} onNote={can.note ? () => setMode("note") : undefined} />
      )}
    </div>
  );
}

/** Why the reply tab is closed and the one way forward (docs/04 §3.1). */
function ReplyBlocked({ conversation: c, onNote }: { conversation: Conversation; onNote: (() => void) | undefined }) {
  const { t } = useTranslation();
  const can = useAbilities(c);
  const names = useMemberNames();
  const { data: session } = useSession();
  const change = useConversationChange(c.id);
  let text: string;
  if (c.status === "closed") text = t("inbox.composer.closed");
  else if (c.assignee_id === null && can.take) text = t("inbox.composer.takeFirst");
  else if (c.assignee_id !== null && c.assignee_id !== session?.user.id)
    text = t("inbox.composer.someoneElse", { name: names.get(c.assignee_id) ?? t("inbox.colleague") });
  else text = t("inbox.composer.noReply");
  return (
    <div className="flex flex-wrap items-center gap-2 rounded-md border border-dashed border-line px-3 py-2.5 text-[13px] text-ink-2">
      <span className="mr-auto">{text}</span>
      {c.status !== "closed" && c.assignee_id === null && can.take && session && (
        <Button
          size="sm"
          variant="primary"
          disabled={change.isPending}
          onClick={() => change.mutate((api) => assignConversation(api, c.id, session.user.id, c.version))}
        >
          {t("inbox.actions.take")}
        </Button>
      )}
      {onNote && (
        <Button size="sm" variant="ghost" onClick={onNote}>
          {t("inbox.composer.writeNote")}
        </Button>
      )}
    </div>
  );
}

/** The newest message in the feed that the signed-in operator did not write; system lines aside. */
function useNewestForeignId(conversationId: string): string | null {
  const { data: session } = useSession();
  const { data } = useMessages(conversationId);
  const me = session?.user.id;
  const newest = data?.pages
    .flatMap((p) => p.items)
    .find((m: Message) => m.kind !== "event" && !(m.sender_type === "operator" && m.sender_id === me));
  return newest?.id ?? null;
}

function sendError(error: unknown, t: TFunction): string {
  if (isApiError(error)) {
    if (error.code === "not_assignee") return t("inbox.composer.errorNotAssignee");
    if (error.code === "conversation_closed") return t("inbox.composer.closed");
    if (error.status === 0) return t("inbox.composer.errorOffline");
  }
  return t("inbox.composer.error");
}
