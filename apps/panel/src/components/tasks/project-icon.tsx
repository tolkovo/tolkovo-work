import type { TagColor, TaskProject } from "@tolkovo/api-client";
import { cn } from "@tolkovo/ui";
import { ChartColumn, Globe, Heart, Monitor, Rocket, Server, ShoppingCart, Smartphone, type LucideIcon } from "lucide-react";
import type { CSSProperties } from "react";

/** The icons a project may have (ADR-0022); without one the tile shows the letters of its key. */
export type ProjectIconKey = NonNullable<TaskProject["icon"]>;

export const PROJECT_ICONS: Record<ProjectIconKey, LucideIcon> = {
  chart: ChartColumn,
  monitor: Monitor,
  server: Server,
  phone: Smartphone,
  cart: ShoppingCart,
  rocket: Rocket,
  heart: Heart,
  globe: Globe,
};

export const PROJECT_ICON_KEYS = Object.keys(PROJECT_ICONS) as ProjectIconKey[];

/** The palette keys of the API as theme colours (packages/ui/tokens.css), for a tile's tint. */
export const PROJECT_COLOR: Record<TagColor, string> = {
  gray: "var(--ink-3)",
  teal: "var(--teal)",
  amber: "var(--lamp)",
  red: "var(--danger)",
  violet: "var(--ai)",
  blue: "var(--tag-blue)",
  green: "var(--tag-green)",
};

/** `--pc`, the project's colour, for the tile and what goes with it (the bar of the open project). */
export function projectColor(color: TagColor): CSSProperties {
  return { "--pc": PROJECT_COLOR[color] } as CSSProperties;
}

type TileProps = {
  icon: TaskProject["icon"];
  color: TagColor;
  projectKey: string;
  /** Filled with the project's colour: the project open now. */
  active?: boolean;
  size?: "sm" | "md";
  className?: string;
};

/** The square of a project: its icon or key on a tint of its colour. Decorative: name the project beside it. */
export function ProjectTile({ icon, color, projectKey, active = false, size = "md", className }: TileProps) {
  const Icon = icon ? PROJECT_ICONS[icon] : null;
  return (
    <span
      aria-hidden="true"
      style={projectColor(color)}
      className={cn(
        "grid shrink-0 place-items-center transition-[border-radius]",
        size === "md" ? "size-[34px] rounded-[10px]" : "size-5 rounded-md",
        active
          ? "bg-(--pc) text-surface"
          : "bg-[color-mix(in_srgb,var(--pc)_16%,transparent)] text-(--pc) shadow-[inset_0_0_0_1px_color-mix(in_srgb,var(--pc)_30%,transparent)]",
        className,
      )}
    >
      {Icon ? (
        <Icon className={size === "md" ? "size-[18px]" : "size-3"} strokeWidth={1.8} />
      ) : (
        <span className={cn("font-mono leading-none font-semibold", size === "md" ? "text-[10.5px]" : "text-[8px]")}>
          {projectKey.slice(0, size === "md" ? 3 : 2)}
        </span>
      )}
    </span>
  );
}
