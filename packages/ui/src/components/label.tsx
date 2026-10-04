import * as LabelPrimitive from "@radix-ui/react-label";
import type { ComponentProps } from "react";

import { cn } from "../lib/cn";

function Label({ className, ...props }: ComponentProps<typeof LabelPrimitive.Root>) {
  return (
    <LabelPrimitive.Root
      data-slot="label"
      className={cn(
        "flex items-center gap-2 text-[12.5px] leading-none font-medium text-ink select-none",
        "peer-disabled:cursor-not-allowed peer-disabled:opacity-55",
        className,
      )}
      {...props}
    />
  );
}

export { Label };
