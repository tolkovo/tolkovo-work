import { Link } from "@tanstack/react-router";
import { Button, Card, CardContent, CardFooter, CardHeader } from "@tolkovo/ui";
import type { FormEvent, ReactNode } from "react";

type AuthCardProps = {
  title: string;
  subtitle: string;
  submitLabel: string;
  switchPrompt: string;
  switchAction: string;
  switchTo: "/login" | "/register";
  children: ReactNode;
};

/**
 * Layout of an auth form. P-01 is markup only: submitting does not send anything yet.
 * P-02 wires the form to the auth API (generated client + zod), with errors and pending state.
 */
export function AuthCard({ title, subtitle, submitLabel, switchPrompt, switchAction, switchTo, children }: AuthCardProps) {
  const onSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
  };

  return (
    <Card className="shadow-pop">
      <CardHeader className="border-b-0 px-6 pt-6 pb-1">
        <h1 className="m-0 font-display text-lg leading-tight font-semibold tracking-[-0.01em]">{title}</h1>
        <p className="m-0 text-[13px] text-ink-2">{subtitle}</p>
      </CardHeader>
      <form onSubmit={onSubmit}>
        <CardContent className="flex flex-col gap-3.5 px-6 pt-4 pb-6">
          {children}
          <Button type="submit" variant="primary" size="lg" className="mt-1 w-full">
            {submitLabel}
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

export function Field({ children }: { children: ReactNode }) {
  return <div className="flex flex-col gap-1.5">{children}</div>;
}
