import { cn } from "@tolkovo/ui";
import { useCallback, useEffect, useId, useRef, useState, type KeyboardEvent, type ReactNode, type RefObject } from "react";

/**
 * A menu button and a popover with the keyboard behaviour of the WAI-ARIA patterns: Escape
 * and a click outside close them and return focus to the trigger. Small on purpose; a
 * headless library is a new dependency (docs/00 §4.3) and these two cover the inbox.
 */

/** Closes on Escape, Tab out and a pointer press outside `box`. */
function useDismiss(open: boolean, box: RefObject<HTMLElement | null>, close: (restoreFocus: boolean) => void) {
  useEffect(() => {
    if (!open) return;
    const onPointer = (e: PointerEvent) => {
      if (box.current && !box.current.contains(e.target as Node)) close(false);
    };
    document.addEventListener("pointerdown", onPointer);
    return () => document.removeEventListener("pointerdown", onPointer);
  }, [open, box, close]);
}

export type MenuItem = {
  label: string;
  onSelect: () => void;
  disabled?: boolean;
  /** Shown after the label, e.g. a hotkey. */
  hint?: string;
  /** A quieter line under the label. */
  detail?: string;
};

type MenuProps = {
  /** The trigger's content; give an icon-only trigger a `label`. */
  trigger: ReactNode;
  label?: string;
  items: MenuItem[];
  align?: "start" | "end";
  triggerClassName?: string;
  disabled?: boolean;
};

export function Menu({ trigger, label, items, align = "end", triggerClassName, disabled }: MenuProps) {
  const [open, setOpen] = useState(false);
  const box = useRef<HTMLDivElement>(null);
  const button = useRef<HTMLButtonElement>(null);
  const list = useRef<HTMLDivElement>(null);
  const id = useId();

  const close = useCallback((restoreFocus: boolean) => {
    setOpen(false);
    if (restoreFocus) button.current?.focus();
  }, []);
  useDismiss(open, box, close);

  const itemsEl = () => [...(list.current?.querySelectorAll<HTMLButtonElement>('[role="menuitem"]:not(:disabled)') ?? [])];
  useEffect(() => {
    if (open) itemsEl()[0]?.focus();
  }, [open]);

  const onKeyDown = (e: KeyboardEvent) => {
    const els = itemsEl();
    const at = els.indexOf(document.activeElement as HTMLButtonElement);
    const move = (i: number) => els[(i + els.length) % els.length]?.focus();
    if (e.key === "ArrowDown") move(at + 1);
    else if (e.key === "ArrowUp") move(at - 1);
    else if (e.key === "Home") move(0);
    else if (e.key === "End") move(els.length - 1);
    else if (e.key === "Escape") close(true);
    else if (e.key === "Tab") close(false);
    else return;
    if (e.key !== "Tab") e.preventDefault();
  };

  return (
    <div ref={box} className="relative">
      <button
        ref={button}
        type="button"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-controls={open ? id : undefined}
        aria-label={label}
        title={label}
        disabled={disabled}
        onClick={() => setOpen((v) => !v)}
        onKeyDown={(e) => {
          if (e.key === "ArrowDown" && !open) {
            e.preventDefault();
            setOpen(true);
          }
        }}
        className={triggerClassName}
      >
        {trigger}
      </button>
      {open && (
        <div
          ref={list}
          id={id}
          role="menu"
          aria-label={label}
          onKeyDown={onKeyDown}
          className={cn(
            "absolute top-full z-20 mt-1 flex min-w-48 flex-col rounded-lg border border-line bg-surface py-1 shadow-[var(--shadow)]",
            align === "end" ? "right-0" : "left-0",
          )}
        >
          {items.map((item) => (
            <button
              key={item.label}
              type="button"
              role="menuitem"
              tabIndex={-1}
              disabled={item.disabled}
              onClick={() => {
                close(true);
                item.onSelect();
              }}
              className="flex items-center justify-between gap-4 px-3 py-1.5 text-left text-[13px] text-ink outline-none hover:bg-hover focus-visible:bg-hover disabled:text-ink-3"
            >
              {item.detail ? (
                <span className="flex flex-col">
                  {item.label}
                  <span className="text-[12px] text-ink-3">
                    <span className="sr-only">,</span> {item.detail}
                  </span>
                </span>
              ) : (
                item.label
              )}
              {item.hint && <kbd className="font-mono text-[11px] text-ink-3">{item.hint}</kbd>}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

type PopoverProps = {
  trigger: ReactNode;
  label: string;
  /** Rendered while open. */
  children: ReactNode;
  triggerClassName?: string;
  align?: "start" | "end";
};

/** A non-modal dialog under its trigger; focus moves to its first field. */
export function Popover({ trigger, label, children, triggerClassName, align = "start" }: PopoverProps) {
  const [open, setOpen] = useState(false);
  const box = useRef<HTMLDivElement>(null);
  const button = useRef<HTMLButtonElement>(null);
  const panel = useRef<HTMLDivElement>(null);
  const id = useId();

  const close = useCallback((restoreFocus: boolean) => {
    setOpen(false);
    if (restoreFocus) button.current?.focus();
  }, []);
  useDismiss(open, box, close);
  useEffect(() => {
    if (open) panel.current?.querySelector<HTMLElement>("input, button")?.focus();
  }, [open]);

  return (
    <div ref={box} className="relative">
      <button
        ref={button}
        type="button"
        aria-haspopup="dialog"
        aria-expanded={open}
        aria-controls={open ? id : undefined}
        onClick={() => setOpen((v) => !v)}
        className={triggerClassName}
      >
        {trigger}
      </button>
      {open && (
        <div
          ref={panel}
          id={id}
          role="dialog"
          aria-label={label}
          onKeyDown={(e) => {
            if (e.key === "Escape") {
              e.preventDefault();
              close(true);
            }
          }}
          className={cn(
            "absolute top-full z-20 mt-1 w-64 rounded-lg border border-line bg-surface p-2 shadow-[var(--shadow)]",
            align === "end" ? "right-0" : "left-0",
          )}
        >
          {children}
        </div>
      )}
    </div>
  );
}
