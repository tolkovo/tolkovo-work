import type { Presence } from "@tolkovo/api-client";
import { cn } from "@tolkovo/ui";

const dot: Record<Presence, string> = {
  online: "bg-teal",
  away: "bg-lamp",
  offline: "border border-ink-3 bg-transparent",
};

/** A member's presence as a dot (docs/07 §1: teal is «Онлайн»); the text says it too. */
export function PresenceDot({ presence, className }: { presence: Presence; className?: string }) {
  return <span aria-hidden="true" className={cn("inline-block size-2 shrink-0 rounded-full", dot[presence], className)} />;
}
