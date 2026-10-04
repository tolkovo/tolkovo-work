import { useMembers, useSession } from "@tolkovo/api-client/react";
import { cn } from "@tolkovo/ui";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";

import { initials } from "../../lib/inbox/tabs";
import { useTypists, useViewers } from "../../lib/realtime/signals";

/** Member names by user id; empty while loading. */
export function useMemberNames(): Map<string, string> {
  const { data } = useMembers();
  return useMemo(() => new Map((data ?? []).map((m) => [m.user_id, m.name])), [data]);
}

/** Colleagues in the conversation and who of them types: the other operators, never me. */
function useColleagues(conversationId: string) {
  const { data: session } = useSession();
  const meId = session?.user.id;
  const viewers = useViewers(conversationId, meId);
  const typists = useTypists(conversationId, meId);
  const typingIds = new Set(typists.filter((t) => t.actor === "operator").map((t) => t.actorId));
  const contactTyping = typists.some((t) => t.actor === "contact");
  // Typing without a viewers list yet (it refreshes every 20 s) still shows the colleague.
  const ids = [...new Set([...viewers, ...[...typingIds].filter((id): id is string => id !== null)])];
  return { ids, typingIds, contactTyping };
}

/**
 * Colleagues in a conversation, for a list row (docs/07 §4): an amber ring means viewing, a red
 * one typing. Color is never alone: the names and the state are in the accessible label.
 */
export function CollaboratorMarks({ conversationId }: { conversationId: string }) {
  const { t } = useTranslation();
  const names = useMemberNames();
  const { ids, typingIds } = useColleagues(conversationId);
  if (ids.length === 0) return null;
  const name = (id: string) => names.get(id) ?? t("inbox.colleague");
  const typing = ids.filter((id) => typingIds.has(id));
  const label = [
    typing.length > 0 ? t("inbox.typing", { names: typing.map(name).join(", "), count: typing.length }) : null,
    ids.length > typing.length
      ? t("inbox.viewing", {
          names: ids
            .filter((id) => !typingIds.has(id))
            .map(name)
            .join(", "),
          count: ids.length - typing.length,
        })
      : null,
  ]
    .filter(Boolean)
    .join(". ");
  return (
    <span role="img" aria-label={label} title={label} className="flex shrink-0 -space-x-1.5">
      {ids.slice(0, 2).map((id) => (
        <span
          key={id}
          aria-hidden="true"
          className={cn(
            "grid size-[22px] place-items-center rounded-full border-2 bg-surface text-[8.5px] font-semibold text-ink-2",
            typingIds.has(id) ? "border-danger" : "border-lamp",
          )}
        >
          {initials(name(id))}
        </span>
      ))}
    </span>
  );
}

/** Under the feed: who else is here and who types, including the customer. */
export function PresenceLine({ conversationId }: { conversationId: string }) {
  const { t } = useTranslation();
  const names = useMemberNames();
  const { ids, typingIds, contactTyping } = useColleagues(conversationId);
  const name = (id: string) => names.get(id) ?? t("inbox.colleague");
  const typing = [...(contactTyping ? [t("inbox.contact")] : []), ...ids.filter((id) => typingIds.has(id)).map(name)];
  const viewing = ids.filter((id) => !typingIds.has(id)).map(name);
  return (
    <div aria-live="polite" className="mt-auto flex min-h-5 flex-wrap items-center gap-x-4 gap-y-1 pt-1 text-xs text-ink-3">
      {typing.length > 0 && (
        <span className="flex items-center gap-1.5">
          <TypingDots />
          {t("inbox.typing", { names: typing.join(", "), count: typing.length })}
        </span>
      )}
      {viewing.length > 0 && (
        <span className="flex items-center gap-1.5">
          <span aria-hidden="true" className="size-2 rounded-full border-2 border-lamp" />
          {t("inbox.viewing", { names: viewing.join(", "), count: viewing.length })}
        </span>
      )}
    </div>
  );
}

function TypingDots() {
  return (
    <span aria-hidden="true" className="inline-flex gap-[3px]">
      {[0, 1, 2].map((i) => (
        <span
          key={i}
          className="size-[5px] animate-pulse rounded-full bg-ink-3 motion-reduce:animate-none"
          style={{ animationDelay: `${i * 200}ms` }}
        />
      ))}
    </span>
  );
}
