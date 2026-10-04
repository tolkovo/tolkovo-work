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
  switchPrompt: string;
  switchAction: string;
  switchTo: "/login" | "/register";
  children: ReactNode;
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
      {/* noValidate: our messages (localized, tied to fields) replace the browser bubbles. */}
      <form onSubmit={submit} noValidate aria-busy={pending}>
        <CardContent className="flex flex-col gap-3.5 px-6 pt-4 pb-6">
          <FormMessage tone="error" message={error} />
          {children}
          <Button type="submit" variant="primary" size="lg" className="mt-1 w-full" disabled={pending}>
            {pending ? pendingLabel : submitLabel}
          </Button>
        </CardContent>
      </form>
      <CardFooter className="justify-center gap-1.5 px-6 text-[13px] text-ink-2">
        <span>{switchPrompt}</span>
        <Link to={switchTo} className="font-medium text-teal underline-offset-4 hover:underline">
          {switchAction}
        </Link>
      </CardFooter>
    </Card>
  );
}
