import { Link } from "@tanstack/react-router";

import { LogoMark, Wordmark } from "../logo";
import { LanguageSwitcher } from "./language-switcher";
import { ThemeSwitcher } from "./theme-switcher";
import { UserMenu } from "./user-menu";
import { WorkspaceSwitcher } from "./workspace-switcher";

/**
 * Top bar (docs/07 §3): workspace, preferences, user. The ⌘K palette, connection state,
 * notifications and status with capacity join it with the features behind them (I-01, R-02).
 */
export function Topbar() {
  return (
    <header className="flex h-12 min-w-0 items-center gap-3 border-b border-line bg-surface px-4 max-nav:gap-2 max-nav:px-3">
      <Link to="/inbox" className="flex items-center gap-2 rounded-md no-underline">
        <LogoMark className="hidden size-6 after:top-[3px] after:right-[3px] after:size-[7px] max-nav:inline-block" />
        <Wordmark />
      </Link>
      <span aria-hidden="true" className="h-5 w-px bg-line max-nav:hidden" />
      <WorkspaceSwitcher />
      <div className="flex-1" />
      <LanguageSwitcher />
      <ThemeSwitcher />
      <UserMenu />
    </header>
  );
}
