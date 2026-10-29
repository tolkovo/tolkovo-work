import type { ComponentProps } from "react";

import { cn } from "../lib/cn";

/**
 * Native select styled like Input. Native on purpose: keyboard, screen readers and mobile
 * pickers work out of the box, and long lists (time zones) need no virtualization. The arrow
 * is ours (--select-chevron in tokens.css), inset from the edge like the rest of the padding.
 */
function Select({ className, ...props }: ComponentProps<"select">) {
  return (
    <select
      data-slot="select"
      className={cn(
        "h-9 w-full min-w-0 rounded-lg border border-input bg-surface pr-8 pl-2.5 text-sm text-ink",
        // Our own chevron: the native one sits flush against the border and differs per browser
        "appearance-none truncate bg-(image:--select-chevron) bg-size-[1rem] bg-position-[right_0.625rem_center] bg-no-repeat",
        "transition-colors",
        "focus-visible:border-ring focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-ring",
        "disabled:cursor-not-allowed disabled:opacity-55",
        "aria-invalid:border-destructive",
        className,
      )}
      {...props}
    />
  );
}

export { Select };
