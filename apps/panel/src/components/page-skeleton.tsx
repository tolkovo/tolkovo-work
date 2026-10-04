import { Skeleton } from "@tolkovo/ui";
import { useTranslation } from "react-i18next";

/** Loading state of a screen (docs/00 §7.4): shown while a route chunk or its data loads. */
export function PageSkeleton() {
  const { t } = useTranslation();
  return (
    <div role="status" className="flex flex-col gap-[18px] px-5 py-[18px] max-nav:px-4">
      <span className="sr-only">{t("app.loading")}</span>
      <Skeleton className="h-6 w-40" />
      <div className="grid grid-cols-4 gap-2.5 max-wide:grid-cols-2">
        <Skeleton className="h-[92px]" />
        <Skeleton className="h-[92px]" />
        <Skeleton className="h-[92px]" />
        <Skeleton className="h-[92px]" />
      </div>
      <Skeleton className="h-64" />
    </div>
  );
}
