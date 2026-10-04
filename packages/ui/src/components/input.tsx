import type { ComponentProps } from "react";

import { cn } from "../lib/cn";

function Input({ className, type = "text", ...props }: ComponentProps<"input">) {
  return (
    <input
      data-slot="input"
      type={type}
      className={cn(
        "h-9 w-full min-w-0 rounded-lg border border-input bg-surface px-2.5 text-sm text-ink",
        "placeholder:text-ink-3 transition-colors",
        "focus-visible:border-ring focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-ring",
        "disabled:cursor-not-allowed disabled:opacity-55",
        "aria-invalid:border-destructive",
        className,
      )}
      {...props}
    />
  );
}

export { Input };
