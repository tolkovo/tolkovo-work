import type { Conversation, IssueDraft, IssueLink, MessageEvent } from "@tolkovo/api-client";
import {
  useConversationIssues,
  useCreateConversationIssue,
  useDismissConversationIssue,
  useIssueDraft,
  useLinkConversationIssue,
  useOpenGitHubIssues,
  useRetryConversationIssue,
} from "@tolkovo/api-client/react";
import { Button, Label, Skeleton, cn } from "@tolkovo/ui";
import { CircleDot, ExternalLink } from "lucide-react";
import { useEffect, useId, useRef, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

import { describeApiError } from "../../lib/api-errors";
import { useAbilities } from "../../lib/inbox/actions";
import { parseIssueNumber, parseLabels } from "../../lib/integrations/github";
import { FormMessage, TextField } from "../form/fields";
import type { MenuItem } from "../menu";

/**
 * GitHub issues from a conversation (GH-02, ADR-0012, docs/04 §3). The operator confirms every
 * issue: the form starts from the workspace's templates, the customer's words go along only
 * when the operator keeps them. Files and contact data never go to GitHub.
 */

export type IssueMode = "create" | "link";

/**
 * "Create issue" and "Link to an issue" for the ⋯ menu of the header: with `issues.create`, when
 * GitHub takes new issues (connected, a repository picked).
 */
export function useIssueMenuItems(c: Conversation, onOpen: (mode: IssueMode) => void): MenuItem[] {
  const { t } = useTranslation();
  const can = useAbilities(c);
  const issues = useConversationIssues(c.id, { enabled: can.issues });
  if (!can.issues || !issues.data?.repository) return [];
  return [
    { label: t("inbox.issues.create"), onSelect: () => onOpen("create") },
    { label: t("inbox.issues.link"), onSelect: () => onOpen("link") },
  ];
}

const control = cn(
  "w-full min-w-0 rounded-lg border border-input bg-surface px-2.5 text-sm text-ink",
  "focus-visible:border-ring focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-ring",
  "disabled:cursor-not-allowed disabled:opacity-55",
);

type IssueFormProps = {
  conversation: Conversation;
  mode: IssueMode;
  onMode: (mode: IssueMode) => void;
  onClose: () => void;
  /** Called after a success with what to tell the operator. */
  onDone: (message: string) => void;
};

/**
 * The issue form in place of the feed: a new issue from the draft, or a link to one that exists.
 * Escape and "Cancel" close it; the heading takes focus when it opens.
 */
export function IssueForm({ conversation: c, mode, onMode, onClose, onDone }: IssueFormProps) {
  const { t } = useTranslation();
  const heading = useRef<HTMLHeadingElement>(null);
  useEffect(() => heading.current?.focus(), [mode]);
  return (
    <section
      aria-labelledby="issue-form-title"
      onKeyDown={(e) => {
        if (e.key === "Escape") {
          e.stopPropagation();
          onClose();
        }
      }}
      className="flex min-h-0 flex-1 flex-col gap-4 overflow-auto bg-paper px-5 py-4 max-nav:px-3"
    >
      <div className="flex flex-wrap items-center gap-3">
        <h3 id="issue-form-title" ref={heading} tabIndex={-1} className="m-0 mr-auto flex items-center gap-2 text-[15px] font-semibold outline-none">
          <CircleDot aria-hidden="true" className="size-4 text-ink-2" />
          {t(mode === "create" ? "inbox.issues.createTitle" : "inbox.issues.linkTitle")}
        </h3>
        <div role="group" aria-label={t("inbox.issues.mode")} className="flex rounded-lg border border-line bg-surface p-0.5">
          {(["create", "link"] as const).map((m) => (
            <button
              key={m}
              type="button"
              aria-pressed={mode === m}
              onClick={() => onMode(m)}
              className="rounded-md px-2.5 py-1 text-[13px] text-ink-2 hover:text-ink aria-pressed:bg-sel aria-pressed:font-medium aria-pressed:text-ink"
            >
              {t(m === "create" ? "inbox.issues.modeCreate" : "inbox.issues.modeLink")}
            </button>
          ))}
        </div>
      </div>
      {mode === "create" ? (
        <CreateIssue conversationId={c.id} onClose={onClose} onDone={onDone} />
      ) : (
        <LinkIssue conversationId={c.id} onClose={onClose} onDone={onDone} />
      )}
    </section>
  );
}

function CreateIssue({ conversationId, onClose, onDone }: { conversationId: string; onClose: () => void; onDone: (m: string) => void }) {
  const { t } = useTranslation();
  const draft = useIssueDraft(conversationId);
  if (draft.isPending) {
    return (
      <div aria-busy="true" aria-label={t("app.loading")} className="flex flex-col gap-3">
        <Skeleton className="h-9" />
        <Skeleton className="h-48" />
      </div>
    );
  }
  if (draft.isError) {
    return (
      <div className="flex flex-col items-start gap-3">
        <FormMessage tone="error" message={describeApiError(t, draft.error).message} />
        <Button size="sm" onClick={onClose}>
          {t("inbox.issues.cancel")}
        </Button>
      </div>
    );
  }
  return <CreateIssueForm conversationId={conversationId} draft={draft.data} onClose={onClose} onDone={onDone} />;
}

function CreateIssueForm({
  conversationId,
  draft,
  onClose,
  onDone,
}: {
  conversationId: string;
  draft: IssueDraft;
  onClose: () => void;
  onDone: (m: string) => void;
}) {
  const { t } = useTranslation();
  const create = useCreateConversationIssue(conversationId);
  const [title, setTitle] = useState(draft.title);
  const [body, setBody] = useState(draft.body);
  // In a public repository everyone reads the issue: the customer's words stay out unless kept.
  const [withQuote, setWithQuote] = useState(draft.repository.private && draft.quote !== "");
  const [quote, setQuote] = useState(draft.quote);
  const [labels, setLabels] = useState(draft.labels.join(", "));
  const [titleError, setTitleError] = useState<string | undefined>();
  const bodyId = useId();
  const quoteId = useId();
  const quoteHint = useId();

  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (!title.trim()) {
      setTitleError(t("inbox.issues.titleRequired"));
      document.getElementById("issue-title")?.focus();
      return;
    }
    setTitleError(undefined);
    create.mutate(
      { title: title.trim(), body, quote: withQuote ? quote : "", labels: parseLabels(labels) },
      { onSuccess: () => onDone(t("inbox.issues.created")) },
    );
  };

  return (
    <form noValidate onSubmit={submit} className="flex max-w-[760px] flex-col gap-4">
      <p className="m-0 text-[13px] text-ink-2">
        {t("inbox.issues.repository")}{" "}
        <a href={draft.repository.url} target="_blank" rel="noreferrer" className="font-mono text-ink">
          {draft.repository.full_name}
        </a>
      </p>
      {!draft.repository.private && (
        <p role="note" className="m-0 rounded-lg border border-lamp bg-lamp-soft px-3 py-2 text-[13px] text-ink">
          {t("inbox.issues.publicRepo")}
        </p>
      )}
      <p className="m-0 rounded-lg border border-note-line bg-note px-3 py-2 text-[13px] text-ink">{t("inbox.issues.dataNote")}</p>
      <TextField
        id="issue-title"
        name="title"
        label={t("inbox.issues.title")}
        value={title}
        maxLength={256}
        error={titleError}
        onChange={(e) => setTitle(e.currentTarget.value.replace(/[\r\n]+/g, " "))}
      />
      <div className="flex flex-col gap-1.5">
        <Label htmlFor={bodyId}>{t("inbox.issues.body")}</Label>
        <textarea
          id={bodyId}
          value={body}
          rows={12}
          maxLength={20000}
          onChange={(e) => setBody(e.currentTarget.value)}
          className={cn(control, "py-2 font-mono text-[12.5px] leading-relaxed")}
        />
      </div>
      {draft.quote !== "" && (
        <div className="flex flex-col gap-1.5">
          <label className="flex items-center gap-2 text-sm font-medium">
            <input type="checkbox" checked={withQuote} onChange={(e) => setWithQuote(e.currentTarget.checked)} className="size-4" />
            {t("inbox.issues.withQuote")}
          </label>
          {withQuote && (
            <>
              <Label htmlFor={quoteId} className="sr-only">
                {t("inbox.issues.quote")}
              </Label>
              <textarea
                id={quoteId}
                value={quote}
                rows={5}
                maxLength={4000}
                aria-describedby={quoteHint}
                onChange={(e) => setQuote(e.currentTarget.value)}
                className={cn(control, "py-2 text-[13px] leading-relaxed")}
              />
              <p id={quoteHint} className="m-0 text-[12.5px] text-ink-3">
                {t("inbox.issues.quoteHint")}
              </p>
            </>
          )}
        </div>
      )}
      <TextField
        id="issue-labels"
        name="labels"
        label={t("inbox.issues.labels")}
        hint={t("inbox.issues.labelsHint")}
        value={labels}
        onChange={(e) => setLabels(e.currentTarget.value)}
      />
      <FormMessage tone="error" message={create.isError ? describeApiError(t, create.error).message : null} />
      <div className="flex flex-wrap gap-2">
        <Button type="submit" variant="primary" disabled={create.isPending}>
          {create.isPending ? t("inbox.issues.creating") : t("inbox.issues.submit")}
        </Button>
        <Button type="button" onClick={onClose}>
          {t("inbox.issues.cancel")}
        </Button>
      </div>
    </form>
  );
}

function LinkIssue({ conversationId, onClose, onDone }: { conversationId: string; onClose: () => void; onDone: (m: string) => void }) {
  const { t } = useTranslation();
  const open = useOpenGitHubIssues();
  const link = useLinkConversationIssue(conversationId);
  const [value, setValue] = useState("");
  const [error, setError] = useState<string | undefined>();
  const listId = useId();

  const submit = (e: FormEvent) => {
    e.preventDefault();
    const number = parseIssueNumber(value);
    if (number === null) {
      setError(t("inbox.issues.numberInvalid"));
      document.getElementById("issue-number")?.focus();
      return;
    }
    setError(undefined);
    link.mutate(number, { onSuccess: (l) => onDone(t("inbox.issues.linked", { number: l.number ?? number })) });
  };

  const items = open.data ?? [];
  return (
    <form noValidate onSubmit={submit} className="flex max-w-[640px] flex-col gap-4">
      <TextField
        id="issue-number"
        name="number"
        label={t("inbox.issues.number")}
        hint={t("inbox.issues.numberHint")}
        value={value}
        inputMode="text"
        autoComplete="off"
        error={error}
        onChange={(e) => setValue(e.currentTarget.value)}
      />
      {items.length > 0 && (
        <fieldset className="m-0 flex flex-col gap-1 border-0 p-0">
          <legend id={listId} className="mb-1.5 p-0 text-[13px] font-medium">
            {t("inbox.issues.openIssues")}
          </legend>
          {items.map((i) => (
            <label key={`${i.repository}#${i.number}`} className="flex min-w-0 items-center gap-2 rounded-md px-2 py-1.5 text-[13px] hover:bg-hover">
              <input
                type="radio"
                name="open-issue"
                checked={parseIssueNumber(value) === i.number}
                onChange={() => setValue(String(i.number))}
              />
              <span className="font-mono text-ink-2">#{i.number}</span>
              <span className="truncate">{i.title}</span>
            </label>
          ))}
        </fieldset>
      )}
      <FormMessage tone="error" message={link.isError ? describeApiError(t, link.error).message : null} />
      <div className="flex flex-wrap gap-2">
        <Button type="submit" variant="primary" disabled={link.isPending}>
          {t("inbox.issues.linkSubmit")}
        </Button>
        <Button type="button" onClick={onClose}>
          {t("inbox.issues.cancel")}
        </Button>
      </div>
    </form>
  );
}

/**
 * The issues of the conversation in the customer card: number, title and status with a link to
 * GitHub; a refused one says why and offers to send it again or remove it.
 */
export function ConversationIssuesList({ conversation: c }: { conversation: Conversation }) {
  const { t } = useTranslation();
  const can = useAbilities(c);
  const issues = useConversationIssues(c.id, { enabled: can.issues });
  if (!can.issues) return null;
  if (issues.isPending) return <Skeleton className="h-10" />;
  if (issues.isError) {
    return (
      <p role="alert" className="m-0 text-[13px] text-ink-2">
        {t("inbox.issues.loadError")}
      </p>
    );
  }
  if (issues.data.items.length === 0) {
    return (
      <p className="m-0 text-[13px] text-ink-3">
        {t(issues.data.repository ? "inbox.issues.none" : "inbox.issues.notConnected")}
      </p>
    );
  }
  return (
    <ul className="m-0 flex list-none flex-col gap-2.5 p-0">
      {issues.data.items.map((l) => (
        <IssueItem key={l.id} conversationId={c.id} issue={l} />
      ))}
    </ul>
  );
}

const STATUS_CLASS: Record<IssueLink["status"], string> = {
  creating: "bg-sunk text-ink-2",
  open: "bg-teal-soft text-ink",
  closed: "bg-sunk text-ink-2",
  failed: "bg-danger-soft text-ink",
};

function IssueItem({ conversationId, issue: l }: { conversationId: string; issue: IssueLink }) {
  const { t } = useTranslation();
  const retry = useRetryConversationIssue(conversationId);
  const dismiss = useDismissConversationIssue(conversationId);
  const failure = retry.error ?? dismiss.error;
  return (
    <li className="flex flex-col gap-1 text-[13px]">
      <div className="flex min-w-0 items-start gap-2">
        <span className={cn("shrink-0 rounded-sm px-1.5 py-0.5 text-[11px] font-medium", STATUS_CLASS[l.status])}>
          {t(`inbox.issues.status.${l.status}`)}
        </span>
        {l.url && l.number !== null ? (
          <a href={l.url} target="_blank" rel="noreferrer" className="min-w-0 break-words text-ink">
            <span className="font-mono text-ink-2">#{l.number}</span> {l.title}
            <ExternalLink aria-hidden="true" className="ml-1 inline size-3 text-ink-3" />
          </a>
        ) : (
          <span className="min-w-0 break-words">{l.title}</span>
        )}
      </div>
      {l.status === "failed" && (
        <div className="flex flex-col items-start gap-1.5 pl-1">
          <p className="m-0 text-ink-2">{t(`inbox.issues.error.${l.error ?? "unavailable"}`)}</p>
          <div className="flex gap-1.5">
            <Button size="sm" disabled={retry.isPending || dismiss.isPending} onClick={() => retry.mutate(l.id)}>
              {t("inbox.issues.retry")}
            </Button>
            <Button size="sm" variant="ghost" disabled={retry.isPending || dismiss.isPending} onClick={() => dismiss.mutate(l.id)}>
              {t("inbox.issues.dismiss")}
            </Button>
          </div>
          {failure && <p role="alert" className="m-0 text-danger">{describeApiError(t, failure).message}</p>}
        </div>
      )}
    </li>
  );
}

/** The feed line of an issue event: «Создан issue #12», linked to GitHub. */
export function IssueEventText({ event: e, by }: { event: MessageEvent; by: string }) {
  const { t } = useTranslation();
  if (!e.issue) return <>{t(`inbox.event.${e.type === "issue_created" ? "issueCreated" : "issueLinked"}`, { number: "" }) + by}</>;
  const label = e.type === "issue_created" ? "inbox.event.issueCreated" : "inbox.event.issueLinked";
  const [before, after] = t(label, { number: "\u0000" }).split("\u0000");
  return (
    <>
      {before}
      <a href={e.issue.url} target="_blank" rel="noreferrer" className="text-ink-2 underline">
        {e.issue.repository}#{e.issue.number}
      </a>
      {after}
      {e.from !== e.to && e.to === "pending" ? t("inbox.event.awaitingFix") : ""}
      {by}
    </>
  );
}
