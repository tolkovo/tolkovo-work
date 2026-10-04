import { cn } from "@tolkovo/ui";
import { useTranslation } from "react-i18next";

/** The signal lamp: a dark disc with an amber light — "a customer is waiting" (docs/07 §1). */
export function LogoMark({ className }: { className?: string }) {
  return (
    <span
      aria-hidden="true"
      className={cn(
        "relative inline-block size-[30px] shrink-0 rounded-full bg-ink",
        "after:absolute after:top-1 after:right-1 after:size-[9px] after:rounded-full after:bg-lamp-glow",
        className,
      )}
    />
  );
}

export function Wordmark({ className }: { className?: string }) {
  const { t } = useTranslation();
  return (
    <span className={cn("font-display text-[15px] font-semibold tracking-[-0.01em] text-ink", className)}>
      {t("app.name")}
    </span>
  );
}
