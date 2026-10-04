import type { ComponentProps } from "react";

import { cn } from "../lib/cn";

/** Loading placeholder (docs/00 §7.4). The pulse stops under prefers-reduced-motion. */
function Skeleton({ className, ...props }: ComponentProps<"div">) {
  return (
    <div
      data-slot="skeleton"
      aria-hidden="true"
      className={cn("animate-pulse rounded-lg bg-sunk motion-reduce:animate-none", className)}
      {...props}
    />
  );
}

export { Skeleton };
