import type { TagColor } from "@tolkovo/api-client";

/** The dot of a tag or a topic: palette keys of the API mapped to theme tokens (packages/ui/tokens.css). */
export const TAG_DOT: Record<TagColor, string> = {
  gray: "bg-ink-3",
  teal: "bg-teal",
  amber: "bg-lamp",
  red: "bg-danger",
  violet: "bg-ai",
  blue: "bg-tag-blue",
  green: "bg-tag-green",
};
