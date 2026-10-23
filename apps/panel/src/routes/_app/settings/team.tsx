import type { Member } from "@tolkovo/api-client";
import { useCan, useMembers, useSession } from "@tolkovo/api-client/react";
import { createFileRoute } from "@tanstack/react-router";
import { Button, Skeleton } from "@tolkovo/ui";
import { useState } from "react";
import { useTranslation } from "react-i18next";

import { FormMessage } from "../../../components/form/fields";
import { PresenceDot } from "../../../components/presence-dot";
import { Invitations } from "../../../components/settings/invitations";
import { MemberRole, RemoveMember } from "../../../components/settings/member-actions";
import { describeApiError } from "../../../lib/api-errors";
import { manageable } from "../../../lib/team";

export const Route = createFileRoute("/_app/settings/team")({
  component: TeamPage,
});

/**
 * Members of the workspace; with `members.manage`, inviting people (A-03), changing roles and
 * removing members from the team (ADR-0020) too.
 */
function TeamPage() {
  const { t } = useTranslation();
  const members = useMembers();
  const { data: session } = useSession();
  const canManage = useCan("members.manage");

  return (
    <section aria-labelledby="settings-team-title" aria-busy={members.isPending} className="flex flex-col gap-3">
      <h2 id="settings-team-title" className="m-0 text-base font-semibold">
        {t("settings.team.title")}
      </h2>
      {members.isPending ? (
        <div role="status" className="flex flex-col gap-2">
          <span className="sr-only">{t("app.loading")}</span>
          <Skeleton className="h-9" />
          <Skeleton className="h-9" />
          <Skeleton className="h-9" />
        </div>
      ) : members.isError ? (
        <div role="alert" className="flex flex-col items-start gap-2">
          <p className="m-0 text-ink-2">{describeApiError(t, members.error).message}</p>
          <Button variant="secondary" onClick={() => void members.refetch()}>
            {t("error.retry")}
          </Button>
        </div>
      ) : (
        <MembersTable members={members.data} currentUserId={session?.user.id} canManage={canManage} />
      )}
      {canManage ? <Invitations /> : null}
    </section>
  );
}

function MembersTable({
  members,
  currentUserId,
  canManage,
}: {
  members: Member[];
  currentUserId: string | undefined;
  canManage: boolean;
}) {
  const { t } = useTranslation();
  const [notice, setNotice] = useState<string | null>(null);
  return (
    <>
      <p className="m-0 text-[13px] text-ink-2">{t("settings.team.count", { count: members.length })}</p>
      <FormMessage tone="success" message={notice} />
      <div className="overflow-x-auto rounded-xl border border-line bg-surface">
        <table className="w-full border-collapse text-left text-[13px]">
          <thead className="bg-sunk text-ink-2">
            <tr>
              <th scope="col" className="px-3 py-2 font-medium">
                {t("settings.team.name")}
              </th>
              <th scope="col" className="px-3 py-2 font-medium">
                {t("settings.team.email")}
              </th>
              <th scope="col" className="px-3 py-2 font-medium">
                {t("settings.team.role")}
              </th>
              <th scope="col" className="px-3 py-2 font-medium">
                {t("settings.team.status")}
              </th>
              {canManage ? (
                <th scope="col" className="px-3 py-2 font-medium">
                  <span className="sr-only">{t("settings.team.actions")}</span>
                </th>
              ) : null}
            </tr>
          </thead>
          <tbody>
            {members.map((m) => (
              <tr key={m.user_id} className="border-t border-line">
                <td className="px-3 py-2 text-ink">
                  {m.name}
                  {m.user_id === currentUserId ? (
                    <span className="ml-1.5 text-ink-3">{t("settings.team.you")}</span>
                  ) : null}
                </td>
                <td className="px-3 py-2 text-ink-2">{m.email}</td>
                <td className="px-3 py-2 text-ink">
                  {canManage && manageable(m, currentUserId) ? (
                    <MemberRole member={m} members={members} onDone={setNotice} />
                  ) : (
                    t(`roles.${m.role}`)
                  )}
                </td>
                <td className={m.active ? "px-3 py-2 text-ink" : "px-3 py-2 text-ink-3"}>
                  {m.active ? (
                    <span className="inline-flex items-center gap-1.5">
                      <PresenceDot presence={m.presence} />
                      {t(`presence.${m.presence}`)}
                    </span>
                  ) : (
                    t("settings.team.inactive")
                  )}
                </td>
                {canManage ? (
                  <td className="px-3 py-2 text-right">
                    {manageable(m, currentUserId) ? <RemoveMember member={m} members={members} onDone={setNotice} /> : null}
                  </td>
                ) : null}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </>
  );
}
