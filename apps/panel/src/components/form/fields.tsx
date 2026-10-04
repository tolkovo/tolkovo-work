import { Input, Label, Select, cn } from "@tolkovo/ui";
import { CircleAlert } from "lucide-react";
import type { ComponentProps, ReactNode } from "react";

type FieldShellProps = {
  id: string;
  label: string;
  hint?: string | undefined;
  error?: string | undefined;
  children: (aria: { "aria-invalid"?: true; "aria-describedby"?: string }) => ReactNode;
};

/**
 * Label + control + hint + error. The error is tied to the control with aria-describedby and
 * aria-invalid, so a screen reader reads it when the field gets focus (WCAG 3.3.1).
 */
function FieldShell({ id, label, hint, error, children }: FieldShellProps) {
  const hintId = hint ? `${id}-hint` : undefined;
  const errorId = error ? `${id}-error` : undefined;
  const describedBy = [errorId, hintId].filter(Boolean).join(" ") || undefined;
  return (
    <div className="flex flex-col gap-1.5">
      <Label htmlFor={id}>{label}</Label>
      {children({
        ...(error ? { "aria-invalid": true as const } : {}),
        ...(describedBy ? { "aria-describedby": describedBy } : {}),
      })}
      {error ? (
        <p id={errorId} className="m-0 flex items-start gap-1 text-[12.5px] text-danger">
          <CircleAlert aria-hidden="true" className="mt-px size-3.5 shrink-0" strokeWidth={2} />
          {error}
        </p>
      ) : null}
      {hint ? (
        <p id={hintId} className="m-0 text-[12.5px] text-ink-3">
          {hint}
        </p>
      ) : null}
    </div>
  );
}

type TextFieldProps = Omit<ComponentProps<"input">, "id" | "name"> & {
  id: string;
  name: string;
  label: string;
  hint?: string | undefined;
  error?: string | undefined;
};

export function TextField({ id, label, hint, error, className, ...input }: TextFieldProps) {
  return (
    <FieldShell id={id} label={label} hint={hint} error={error}>
      {(aria) => <Input id={id} className={className} {...input} {...aria} />}
    </FieldShell>
  );
}

type SelectFieldProps = Omit<ComponentProps<"select">, "id" | "name"> & {
  id: string;
  name: string;
  label: string;
  hint?: string | undefined;
  error?: string | undefined;
};

export function SelectField({ id, label, hint, error, children, ...select }: SelectFieldProps) {
  return (
    <FieldShell id={id} label={label} hint={hint} error={error}>
      {(aria) => (
        <Select id={id} {...select} {...aria}>
          {children}
        </Select>
      )}
    </FieldShell>
  );
}

/**
 * Form-level message. The live region is always in the DOM, so changing its text is announced
 * (role="alert" for errors, role="status" for success).
 */
export function FormMessage({ tone, message }: { tone: "error" | "success"; message: string | null }) {
  return (
    <div
      role={tone === "error" ? "alert" : "status"}
      className={cn(
        message ? "rounded-lg border px-3 py-2 text-[13px]" : "sr-only",
        // Text stays --ink: --danger on --danger-soft is below AA for small text.
        message && tone === "error" && "border-danger bg-danger-soft text-ink",
        message && tone === "success" && "border-teal bg-teal-soft text-ink",
      )}
    >
      {message}
    </div>
  );
}
