import { Card, CardContent, CardHeader } from "@tolkovo/ui";
import type { ReactNode } from "react";

/** A card of the link pages without a form: a title and what happened or what to do next. */
export function MessageCard({ title, children }: { title: string; children: ReactNode }) {
  return (
    <Card className="shadow-pop">
      <CardHeader className="border-b-0 px-6 pt-6 pb-1">
        <h1 className="m-0 font-display text-lg leading-tight font-semibold tracking-[-0.01em]">{title}</h1>
      </CardHeader>
      <CardContent className="flex flex-col items-start gap-3.5 px-6 pt-3 pb-6 text-[13px] text-ink-2">
        {children}
      </CardContent>
    </Card>
  );
}
