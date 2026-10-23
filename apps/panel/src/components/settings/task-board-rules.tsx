import type { TaskColumnRule } from "@tolkovo/api-client";
import { useTaskBoardSettings, useTaskQueues, useUpdateTaskBoardSettings } from "@tolkovo/api-client/react";
import { Button, Select, Skeleton } from "@tolkovo/ui";
import { X } from "lucide-react";
import { useId, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

import { describeApiError } from "../../lib/api-errors";
import { FormMessage } from "../form/fields";
import { control } from "../tasks/task-bits";

const STATUSES: readonly TaskColumnRule["status"][] = ["open", "in_progress", "review", "verified"];

/** A cell of the table as typed: empty means no limit or no threshold. */
type Cell = { limit: string; age: string };

const cellKey = (queue: string, status: string) => `${queue}:${status}`;

/**
 * «Доска» (ADR-0019, K2): soft limits and age thresholds of the open columns, for all tasks and,
 * when asked, for a queue of its own. Nothing is refused by them: the board only shows them.
 */
export function BoardRules() {
  const { t } = useTranslation();
  const settings = useTaskBoardSettings();
  return (
    <section aria-labelledby="settings-board-title" className="flex flex-col gap-3">
      <h3 id="settings-board-title" className="m-0 text-[14px] font-semibold">
        {t("settings.tasks.board.title")}
      </h3>
      <p className="m-0 text-[13px] text-ink-2">{t("settings.tasks.board.text")}</p>
      {settings.isPending ? (
        <Skeleton className="h-32" />
      ) : settings.isError ? (
        <p role="alert" className="m-0 text-ink-2">
          {describeApiError(t, settings.error).message}
        </p>
      ) : (
        <RulesForm rules={settings.data.rules} />
      )}
    </section>
  );
}

function RulesForm({ rules }: { rules: TaskColumnRule[] }) {
  const { t } = useTranslation();
  const { data: queues } = useTaskQueues();
  const update = useUpdateTaskBoardSettings();
  const pickId = useId();
  const hintId = useId();
  const [cells, setCells] = useState<Record<string, Cell>>(() =>
    Object.fromEntries(rules.map((r) => [cellKey(r.queue_id ?? "", r.status), { limit: String(r.wip_limit ?? ""), age: String(r.age_days ?? "") }])),
  );
  const [rows, setRows] = useState<string[]>(() => ["", ...new Set(rules.flatMap((r) => (r.queue_id ? [r.queue_id] : [])))]);
  const [saved, setSaved] = useState<string | null>(null);
  const queueName = (id: string) => (id ? (queues?.find((q) => q.id === id)?.name ?? t("tasks.queueDeleted")) : t("settings.tasks.board.workspace"));
  const set = (key: string, patch: Partial<Cell>) => setCells((c) => ({ ...c, [key]: { limit: "", age: "", ...c[key], ...patch } }));
  const free = (queues ?? []).filter((q) => !rows.includes(q.id));

  const submit = (e: FormEvent) => {
    e.preventDefault();
    setSaved(null);
    const out: TaskColumnRule[] = [];
    for (const queue of rows) {
      for (const status of STATUSES) {
        const cell = cells[cellKey(queue, status)];
        const limit = cell?.limit.trim() ? Number(cell.limit) : null;
        const age = cell?.age.trim() ? Number(cell.age) : null;
        if (limit !== null || age !== null) out.push({ queue_id: queue || null, status, wip_limit: limit, age_days: age });
      }
    }
    update.mutate({ rules: out }, { onSuccess: () => setSaved(t("settings.tasks.board.saved")) });
  };

  return (
    <form noValidate onSubmit={submit} className="flex flex-col gap-3 rounded-xl border border-line bg-surface p-4">
      <div className="overflow-x-auto">
        <table className="w-full border-collapse text-[13px]" aria-describedby={hintId}>
          <thead>
            <tr className="text-left text-[12px] text-ink-3">
              <th scope="col" className="py-1 pr-3 font-medium">
                {t("settings.tasks.board.scope")}
              </th>
              {STATUSES.map((s) => (
                <th key={s} scope="col" className="px-1 py-1 font-medium whitespace-nowrap">
                  {t(`tasks.board.column.${s}`)}
                  <span className="block text-[11px] font-normal">{t("settings.tasks.board.fields")}</span>
                </th>
              ))}
              <td />
            </tr>
          </thead>
          <tbody>
            {rows.map((queue) => (
              <tr key={queue} className="border-t border-line-2">
                <th scope="row" className="py-2 pr-3 text-left font-medium whitespace-nowrap">
                  {queueName(queue)}
                </th>
                {STATUSES.map((status) => {
                  const key = cellKey(queue, status);
                  const scope = queueName(queue);
                  const column = t(`tasks.board.column.${status}`);
                  return (
                    <td key={status} className="px-1 py-2">
                      <span className="flex items-center gap-1">
                        <input
                          type="number"
                          inputMode="numeric"
                          min={1}
                          max={999}
                          placeholder="—"
                          aria-label={t("settings.tasks.board.limitLabel", { column, scope })}
                          value={cells[key]?.limit ?? ""}
                          onChange={(e) => set(key, { limit: e.currentTarget.value })}
                          className={`${control} h-8 w-16 px-2`}
                        />
                        <input
                          type="number"
                          inputMode="numeric"
                          min={1}
                          max={365}
                          placeholder="—"
                          aria-label={t("settings.tasks.board.ageLabel", { column, scope })}
                          value={cells[key]?.age ?? ""}
                          onChange={(e) => set(key, { age: e.currentTarget.value })}
                          className={`${control} h-8 w-16 px-2`}
                        />
                      </span>
                    </td>
                  );
                })}
                <td className="py-2 pl-1">
                  {queue && (
                    <Button
                      type="button"
                      size="sm"
                      variant="ghost"
                      aria-label={t("settings.tasks.board.removeQueue", { name: queueName(queue) })}
                      title={t("settings.tasks.board.removeQueue", { name: queueName(queue) })}
                      onClick={() => setRows((r) => r.filter((x) => x !== queue))}
                    >
                      <X aria-hidden="true" className="size-4" />
                    </Button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <p id={hintId} className="m-0 text-[12.5px] text-ink-3">
        {t("settings.tasks.board.hint")}
      </p>
      {free.length > 0 && (
        <div className="flex items-center gap-2">
          <label htmlFor={pickId} className="text-[13px] text-ink-2">
            {t("settings.tasks.board.addQueue")}
          </label>
          <Select
            id={pickId}
            value=""
            className="w-56"
            onChange={(e) => {
              const id = e.currentTarget.value;
              if (id) setRows((r) => [...r, id]);
            }}
          >
            <option value="">{t("settings.tasks.board.pickQueue")}</option>
            {free.map((q) => (
              <option key={q.id} value={q.id}>
                {q.name}
              </option>
            ))}
          </Select>
        </div>
      )}
      <FormMessage tone="error" message={update.isError ? describeApiError(t, update.error).message : null} />
      <FormMessage tone="success" message={saved} />
      <Button type="submit" variant="primary" className="self-start" disabled={update.isPending}>
        {t("settings.tasks.board.save")}
      </Button>
    </form>
  );
}
