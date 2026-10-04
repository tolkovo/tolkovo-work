import { clsx, type ClassValue } from "clsx";
import { extendTailwindMerge } from "tailwind-merge";

// Token color names are not Tailwind defaults, so tailwind-merge needs to know them
// to resolve conflicts like `text-ink text-teal-ink` correctly.
const colors = [
  "paper", "surface", "sunk", "hover", "sel",
  "ink", "ink-2", "ink-3", "line", "line-2",
  "teal", "teal-ink", "teal-soft",
  "lamp", "lamp-glow", "lamp-soft", "lamp-ink", "lamp-on",
  "ai", "ai-soft", "ai-ink",
  "danger", "danger-soft", "danger-ink",
  "note", "note-line",
  "background", "foreground", "card", "card-foreground",
  "primary", "primary-foreground", "secondary", "secondary-foreground",
  "muted", "muted-foreground", "accent", "accent-foreground",
  "destructive", "destructive-foreground", "border", "input", "ring",
  "transparent", "current",
];

const twMerge = extendTailwindMerge({
  override: { theme: { color: colors } },
});

/** Joins class names and resolves Tailwind conflicts (later wins). */
export function cn(...inputs: ClassValue[]): string {
  return twMerge(clsx(inputs));
}
