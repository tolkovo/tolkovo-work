import { Link } from "@tanstack/react-router";
import { Button, Card, CardContent, CardFooter, CardHeader } from "@tolkovo/ui";
import type { FormEvent, ReactNode } from "react";

import { FormMessage } from "../form/fields";

type AuthCardProps = {
  title: string;
  subtitle: string;
  submitLabel: string;
  pendingLabel: string;
  pending: boolean;
  /** Form-level error (wrong password, rate limit, no connection). */
  error: string | null;
  onSubmit: (form: HTMLFormElement) => void;
  /** The link to another auth screen under the card; none when absent. */
  switchPrompt?: string;
  switchAction?: string;
  switchTo?: "/login" | "/register" | "/forgot-password";
  /** Shown instead of the form, e.g. after the request succeeded. */
  done?: ReactNode;
  children?: ReactNode;
};

/** An auth form: title, fields, one primary action, a link to the other auth screen. */
export function AuthCard({
  title,
  subtitle,
  submitLabel,
  pendingLabel,
  pending,
  error,
  onSubmit,
  switchPrompt,
  switchAction,
  switchTo,
  done,
  children,
}: AuthCardProps) {
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!pending) onSubmit(event.currentTarget);
  };

  return (
    <Card className="shadow-pop">
      <CardHeader className="border-b-0 px-6 pt-6 pb-1">
        <h1 className="m-0 font-display text-lg leading-tight font-semibold tracking-[-0.01em]">{title}</h1>
        <p className="m-0 text-[13px] text-ink-2">{subtitle}</p>
      </CardHeader>
      {done ? (
        <CardContent className="flex flex-col gap-3.5 px-6 pt-4 pb-6">{done}</CardContent>
      ) : (
        /* noValidate: our messages (localized, tied to fields) replace the browser bubbles. */
        <form onSubmit={submit} noValidate aria-busy={pending}>
          <CardContent className="flex flex-col gap-3.5 px-6 pt-4 pb-6">
            <FormMessage tone="error" message={error} />
            {children}
            <Button type="submit" variant="primary" size="lg" className="mt-1 w-full" disabled={pending}>
              {pending ? pendingLabel : submitLabel}
            </Button>
          </CardContent>
        </form>
      )}
      {switchTo && switchAction ? (
        <CardFooter className="justify-center gap-1.5 px-6 text-[13px] text-ink-2">
          {switchPrompt ? <span>{switchPrompt}</span> : null}
          <Link to={switchTo} className="font-medium text-teal underline-offset-4 hover:underline">
            {switchAction}
          </Link>
        </CardFooter>
      ) : null}
    </Card>
  );
}
