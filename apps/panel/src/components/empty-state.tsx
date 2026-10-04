import type { LucideIcon } from "lucide-react";
import type { ReactNode } from "react";

type EmptyStateProps = {
  icon: LucideIcon;
  title: string;
  text: string;
  /** The call to action: what to do so this screen gets data (docs/00 §7.4). */
  action: ReactNode;
};

export function EmptyState({ icon: Icon, title, text, action }: EmptyStateProps) {
  return (
    <section
      aria-labelledby="empty-state-title"
      className="mx-auto flex max-w-[440px] flex-col items-center gap-3 px-4 py-12 text-center"
    >
      <span className="grid size-12 place-items-center rounded-full bg-sunk text-ink-2">
        <Icon aria-hidden="true" className="size-6" strokeWidth={1.7} />
      </span>
      <h2 id="empty-state-title" className="m-0 text-base font-semibold text-ink">
        {title}
      </h2>
      <p className="m-0 text-ink-2">{text}</p>
      <div className="mt-1">{action}</div>
    </section>
  );
}
