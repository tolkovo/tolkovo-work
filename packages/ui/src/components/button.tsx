import { Slot } from "@radix-ui/react-slot";
import { cva, type VariantProps } from "class-variance-authority";
import type { ComponentProps } from "react";

import { cn } from "../lib/cn";

const buttonVariants = cva(
  [
    "inline-flex shrink-0 items-center justify-center gap-1.5 whitespace-nowrap rounded-md border font-medium",
    "text-[13px] transition-colors select-none",
    "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring",
    "disabled:pointer-events-none disabled:opacity-55",
    "[&_svg]:pointer-events-none [&_svg]:size-4 [&_svg]:shrink-0",
  ],
  {
    variants: {
      variant: {
        // Teal is the operator's color: the main action on a screen.
        primary: "border-primary bg-primary text-primary-foreground hover:brightness-106",
        secondary: "border-line bg-surface text-ink hover:bg-hover",
        ghost: "border-transparent bg-transparent text-ink-2 hover:bg-hover hover:text-ink",
        destructive: "border-destructive bg-destructive text-destructive-foreground hover:brightness-106",
        link: "border-transparent bg-transparent px-0 text-teal underline-offset-4 hover:underline",
      },
      size: {
        sm: "h-[26px] px-2.5",
        md: "h-[30px] px-3",
        lg: "h-9 px-4 text-sm",
        icon: "size-8 p-0",
      },
    },
    defaultVariants: { variant: "secondary", size: "md" },
  },
);

type ButtonProps = ComponentProps<"button"> &
  VariantProps<typeof buttonVariants> & {
    /** Render the single child (e.g. a router link) with button styles. */
    asChild?: boolean;
  };

function Button({ className, variant, size, asChild = false, type, ...props }: ButtonProps) {
  const Comp = asChild ? Slot : "button";
  return (
    <Comp
      data-slot="button"
      className={cn(buttonVariants({ variant, size }), className)}
      // A plain <button> defaults to type="submit"; make the safe choice explicit.
      type={asChild ? type : (type ?? "button")}
      {...props}
    />
  );
}

export { Button, buttonVariants, type ButtonProps };
