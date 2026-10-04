import { useQueryErrorResetBoundary } from "@tanstack/react-query";
import { useRouter, type ErrorComponentProps } from "@tanstack/react-router";
import { Button } from "@tolkovo/ui";
import { TriangleAlert } from "lucide-react";
import { useTranslation } from "react-i18next";

/**
 * Error state (docs/00 §7.4): what happened and what to do. Technical details are not shown:
 * they may contain data of other people; the router already logs the error to the console.
 */
export function ErrorView({ reset }: Pick<ErrorComponentProps, "reset">) {
  const { t } = useTranslation();
  const router = useRouter();
  const queryErrorReset = useQueryErrorResetBoundary();

  const retry = () => {
    queryErrorReset.reset();
    reset();
    void router.invalidate();
  };

  return (
    <section
      role="alert"
      aria-labelledby="error-title"
      className="mx-auto flex max-w-[440px] flex-col items-center gap-3 px-4 py-12 text-center"
    >
      <span className="grid size-12 place-items-center rounded-full bg-danger-soft text-danger">
        <TriangleAlert aria-hidden="true" className="size-6" strokeWidth={1.7} />
      </span>
      <h1 id="error-title" className="m-0 text-base font-semibold text-ink">
        {t("error.title")}
      </h1>
      <p className="m-0 text-ink-2">{t("error.text")}</p>
      <div className="mt-1 flex flex-wrap justify-center gap-2">
        <Button variant="primary" size="lg" onClick={retry}>
          {t("error.retry")}
        </Button>
        <Button variant="secondary" size="lg" onClick={() => window.location.reload()}>
          {t("error.reload")}
        </Button>
      </div>
    </section>
  );
}
