import { create } from "zustand";

/**
 * Unsent composer text per conversation (docs/04 §3.1): switching to another conversation and
 * back keeps it, and so does switching between reply and note. UI state, so it lives in a
 * small store (docs/00 §7.2); it is kept in memory only and dropped with the session. Drafts
 * that survive a reload or follow the operator to another device come later.
 */

export type ComposerMode = "reply" | "note";

export type Draft = {
  text: string;
  mode: ComposerMode;
  /**
   * Idempotency-Key of the send in flight or the last failed one: a retry of the same text
   * reuses it, so the server never posts the message twice (docs/00 §8).
   */
  key: string | null;
  /**
   * The newest message from someone else the operator had seen when they started typing; a
   * newer one pauses the send (collision guard, docs/07 §4).
   */
  seenId: string | null;
};

export const EMPTY_DRAFT: Draft = { text: "", mode: "reply", key: null, seenId: null };

type DraftState = { drafts: Record<string, Draft> };

export const useDraftStore = create<DraftState>(() => ({ drafts: {} }));

export function useDraft(conversationId: string): Draft {
  return useDraftStore((s) => s.drafts[conversationId] ?? EMPTY_DRAFT);
}

export function updateDraft(conversationId: string, patch: Partial<Draft>): void {
  useDraftStore.setState((s) => ({
    drafts: { ...s.drafts, [conversationId]: { ...(s.drafts[conversationId] ?? EMPTY_DRAFT), ...patch } },
  }));
}

/** After a successful send: the text goes, the mode stays. */
export function clearDraftText(conversationId: string): void {
  updateDraft(conversationId, { text: "", key: null, seenId: null });
}

/** Another session (or none): drafts of the previous one must not be shown. */
export function resetDrafts(): void {
  useDraftStore.setState({ drafts: {} });
}
