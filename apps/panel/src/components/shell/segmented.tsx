import { cn } from "@tolkovo/ui";
import type { ReactNode } from "react";

type Option<T extends string> = {
  value: T;
  /** Tooltip; also the accessible name when `content` is an icon. */
  label: string;
  /** Visible content: an icon, or short text (then the text itself is the accessible name). */
  content: ReactNode;
  iconOnly?: boolean;
  lang?: string;
};

type SegmentedProps<T extends string> = {
  label: string;
  value: T;
  options: readonly Option<T>[];
  onChange: (value: T) => void;
};

/** A small group of toggle buttons; the pressed one is the current value. */
export function Segmented<T extends string>({ label, value, options, onChange }: SegmentedProps<T>) {
  return (
    <div
      role="group"
      aria-label={label}
      className="inline-flex shrink-0 rounded-md border border-line bg-surface p-0.5"
    >
      {options.map((option) => (
        <button
          key={option.value}
          type="button"
          aria-pressed={option.value === value}
          aria-label={option.iconOnly ? option.label : undefined}
          title={option.label}
          lang={option.lang}
          onClick={() => onChange(option.value)}
          className={cn(
            "grid h-6 min-w-7 place-items-center rounded-[5px] px-1.5 font-mono text-[11px] font-medium text-ink-2",
            "hover:bg-hover hover:text-ink",
            "aria-pressed:bg-ink aria-pressed:text-paper",
            "[&_svg]:size-[15px]",
          )}
        >
          {option.content}
        </button>
      ))}
    </div>
  );
}
