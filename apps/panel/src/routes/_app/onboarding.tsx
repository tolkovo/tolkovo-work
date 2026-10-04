import { useSession } from "@tolkovo/api-client/react";
import { Link, createFileRoute } from "@tanstack/react-router";
import { Button, Card, CardContent, CardHeader, cn } from "@tolkovo/ui";
import { Check } from "lucide-react";
import { useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";

import { WorkspaceForm } from "../../components/settings/workspace-form";

export const Route = createFileRoute("/_app/onboarding")({
  component: OnboardingPage,
});

/**
 * "Getting started" right after registration (docs/07 §7). Only steps the server supports
 * today: the workspace exists, and its language and time zone are confirmed. Channels and
 * invitations (A-03) join the checklist with their backlog tasks (docs/06).
 */
function OnboardingPage() {
  const { t } = useTranslation();
  const { data: session } = useSession();
  const [confirmed, setConfirmed] = useState(false);
  if (!session) return null;
  const done = confirmed ? 2 : 1;

  return (
    <div className="mx-auto flex w-full max-w-[640px] flex-col gap-[18px] px-5 py-[18px] max-nav:px-4">
      <div className="flex flex-col gap-1">
        <h1 className="m-0 font-display text-lg leading-tight font-semibold tracking-[-0.01em]">
          {t("onboarding.title")}
        </h1>
        <p className="m-0 text-ink-2">{t("onboarding.subtitle", { name: session.workspace.name })}</p>
        <p className="m-0 font-mono text-[12.5px] text-ink-3 tabular-nums">
          {t("onboarding.progress", { done, total: 2 })}
        </p>
      </div>
      <ol className="m-0 flex list-none flex-col gap-3 p-0">
        <Step done title={t("onboarding.created.title")}>
          <p className="m-0 text-[13px] text-ink-2">
            {t("onboarding.created.text", { slug: session.workspace.slug })}
          </p>
        </Step>
        <Step done={confirmed} title={t("onboarding.locale.title")}>
          <p className="m-0 mb-3 text-[13px] text-ink-2">{t("onboarding.locale.text")}</p>
          <WorkspaceForm
            workspace={session.workspace}
            fields={["locale", "timezone"]}
            submitLabel={t("onboarding.locale.submit")}
            onSaved={() => setConfirmed(true)}
          />
        </Step>
      </ol>
      <div>
        <Button asChild variant={confirmed ? "primary" : "secondary"} size="lg">
          <Link to="/inbox">{t("onboarding.finish")}</Link>
        </Button>
      </div>
    </div>
  );
}

function Step({ done, title, children }: { done: boolean; title: string; children: ReactNode }) {
  const { t } = useTranslation();
  return (
    <li>
      <Card>
        <CardHeader className="flex-row items-center gap-2.5">
          <span
            aria-hidden="true"
            className={cn(
              "grid size-6 shrink-0 place-items-center rounded-full border",
              done ? "border-teal bg-teal text-teal-ink" : "border-line bg-surface text-transparent",
            )}
          >
            <Check className="size-3.5" strokeWidth={2.5} />
          </span>
          <h2 className="m-0 text-sm font-semibold">{title}</h2>
          <span className="sr-only">{done ? t("onboarding.stepDone") : t("onboarding.stepTodo")}</span>
        </CardHeader>
        <CardContent>{children}</CardContent>
      </Card>
    </li>
  );
}
