import { useMyWorkspaces, useSession, useSwitchWorkspace } from "@tolkovo/api-client/react";
import { useNavigate } from "@tanstack/react-router";
import { Select } from "@tolkovo/ui";
import { useTranslation } from "react-i18next";

import { describeApiError } from "../../lib/api-errors";

/**
 * Current workspace in the top bar (docs/04 §2.2). A plain name with one workspace; a native
 * select with several. Switching issues a new session and drops cached data of the old one.
 */
export function WorkspaceSwitcher() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { data: session } = useSession();
  const { data: workspaces } = useMyWorkspaces({ enabled: Boolean(session) });
  const switchWorkspace = useSwitchWorkspace();

  if (!session) return null;
  const current = session.workspace;

  if (!workspaces || workspaces.length < 2) {
    return (
      <span className="max-w-[24ch] truncate text-[13px] font-medium text-ink" title={current.name}>
        {current.name}
      </span>
    );
  }

  return (
    <div className="flex min-w-0 items-center gap-2">
      <label htmlFor="workspace-switcher" className="sr-only">
        {t("workspace.switcher.label")}
      </label>
      <Select
        id="workspace-switcher"
        className="h-[30px] max-w-[26ch] text-[13px] font-medium"
        value={current.id}
        disabled={switchWorkspace.isPending}
        aria-describedby={switchWorkspace.isError ? "workspace-switcher-error" : undefined}
        onChange={(event) =>
          switchWorkspace.mutate(event.currentTarget.value, {
            onSuccess: () => void navigate({ to: "/inbox" }),
          })
        }
      >
        {workspaces.map((ws) => (
          <option key={ws.id} value={ws.id}>
            {ws.name}
          </option>
        ))}
      </Select>
      <span id="workspace-switcher-error" role="alert" className="text-[12.5px] text-danger max-nav:sr-only">
        {switchWorkspace.isError ? describeApiError(t, switchWorkspace.error).message : null}
      </span>
    </div>
  );
}
