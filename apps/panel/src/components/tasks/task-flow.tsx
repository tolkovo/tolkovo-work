import type { TaskFlow, TaskFlowFilter } from "@tolkovo/api-client";
import { useTaskFlow } from "@tolkovo/api-client/react";
import { Link, useNavigate } from "@tanstack/react-router";
import { Button, Select, Skeleton } from "@tolkovo/ui";
import { Lock } from "lucide-react";
import { useEffect, useId, useMemo, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";

import { describeApiError } from "../../lib/api-errors";
import { FLOW_DAYS, type TasksSearch } from "../../lib/tasks/tasks";
import { AssigneeFilter } from "./task-board-parts";
import { LayoutSwitch, TaskFilters } from "./task-filters";

/** The statuses of the cumulative flow, bottom up, each with its categorical slot (ADR-0019, K3). */
const BANDS = [
  { key: "done", color: "var(--color-chart-3)" },
  { key: "verified", color: "var(--color-chart-5)" },
  { key: "review", color: "var(--color-chart-4)" },
  { key: "in_progress", color: "var(--color-chart-1)" },
  { key: "open", color: "var(--color-chart-2)" },
] as const;

const H = 180;
const PAD = { top: 8, right: 8, bottom: 22, left: 30 };

export function flowFilter({ view, queue, topic, tag, days }: TasksSearch): TaskFlowFilter {
  const filter: TaskFlowFilter = { days: days ?? 30 };
  if (view === "mine") filter.assignee = "me";
  if (view === "unassigned") filter.assignee = "none";
  if (queue) filter.queue = queue;
  if (topic) filter.topic = topic;
  if (tag) filter.tag = tag;
  return filter;
}

/**
 * «Поток» of the tasks section (ADR-0019, K3): how long tasks take from in progress to done,
 * how many get done a week, how the statuses pile up day by day and which open tasks stand
 * longest. Read from the history of statuses for the last 30 or 90 days, with the board's filters.
 */
export default function TaskFlowView({ search }: { search: TasksSearch }) {
  const { t } = useTranslation();
  const filter = useMemo(() => flowFilter(search), [search]);
  const flow = useTaskFlow(filter);
  return (
    <section aria-label={t("tasks.flow.label")} className="flex min-h-0 min-w-0 flex-1 flex-col">
      <header className="flex flex-wrap items-center gap-2 border-b border-line bg-surface px-4 py-2.5 max-nav:px-3">
        <h1 className="m-0 mr-1 font-display text-base font-semibold max-nav:mr-auto">{t("pages.tasks.title")}</h1>
        <LayoutSwitch search={search} layout="flow" />
        <div className="contents max-nav:order-last max-nav:flex max-nav:w-full max-nav:gap-2 max-nav:overflow-x-auto">
          <PeriodFilter search={search} />
          <AssigneeFilter search={search} />
          <TaskFilters search={search} compact facetsOnly className="contents *:w-40 *:shrink-0" />
        </div>
      </header>
      <div className="min-h-0 flex-1 overflow-y-auto bg-paper p-4 max-nav:p-3">
        {flow.error ? (
          <div role="alert" className="flex flex-col items-start gap-2">
            <p className="m-0 text-ink-2">{describeApiError(t, flow.error).message}</p>
            <Button variant="secondary" onClick={() => void flow.refetch()}>
              {t("error.retry")}
            </Button>
          </div>
        ) : flow.data ? (
          <FlowBody flow={flow.data} search={search} />
        ) : (
          <div role="status" className="grid gap-4 wide:grid-cols-2">
            <span className="sr-only">{t("app.loading")}</span>
            <Skeleton className="h-64" />
            <Skeleton className="h-64" />
            <Skeleton className="h-64" />
            <Skeleton className="h-64" />
          </div>
        )}
      </div>
    </section>
  );
}

function PeriodFilter({ search }: { search: TasksSearch }) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const id = useId();
  return (
    <div className="w-32 min-w-0 shrink-0">
      <label htmlFor={id} className="sr-only">
        {t("tasks.flow.period")}
      </label>
      <Select
        id={id}
        value={String(search.days ?? 30)}
        onChange={(e) => {
          const days = Number(e.currentTarget.value) as TasksSearch["days"];
          void navigate({ to: ".", search: (s: TasksSearch) => ({ ...s, days: days === 30 ? undefined : days }) });
        }}
      >
        {FLOW_DAYS.map((d) => (
          <option key={d} value={d}>
            {t("tasks.flow.days", { count: d })}
          </option>
        ))}
      </Select>
    </div>
  );
}

function FlowBody({ flow, search }: { flow: TaskFlow; search: TasksSearch }) {
  const { t, i18n } = useTranslation();
  const date = useMemo(() => new Intl.DateTimeFormat(i18n.language, { day: "numeric", month: "short", timeZone: "UTC" }), [i18n.language]);
  const day = (iso: string) => date.format(new Date(`${iso.slice(0, 10)}T00:00:00Z`));
  const days = (n: number) => t("tasks.flow.daysShort", { count: n });
  const empty = flow.cfd.every((d) => d.open + d.in_progress + d.review + d.verified + d.done === 0);
  if (empty) {
    return (
      <div className="m-auto flex max-w-[380px] flex-col items-center gap-2 p-6 text-center">
        <h2 className="m-0 text-base font-semibold">{t("tasks.flow.empty.title")}</h2>
        <p className="m-0 text-ink-2">{t("tasks.flow.empty.text")}</p>
      </div>
    );
  }
  return (
    <div className="grid items-start gap-4 wide:grid-cols-2">
      {flow.truncated ? <p className="m-0 text-[12.5px] text-ink-3 wide:col-span-2">{t("tasks.flow.truncated")}</p> : null}
      <Card
        title={t("tasks.flow.cycle.title")}
        lead={
          flow.cycle_p50 === null || flow.cycle_p85 === null
            ? t("tasks.flow.cycle.none")
            : t("tasks.flow.cycle.lead", { p50: days(flow.cycle_p50), p85: days(flow.cycle_p85), count: flow.cycle.length })
        }
      >
        <CycleChart flow={flow} day={day} days={days} />
      </Card>
      <Card title={t("tasks.flow.throughput.title")} lead={t("tasks.flow.throughput.lead", { count: flow.throughput.reduce((n, w) => n + w.done, 0) })}>
        <ThroughputChart flow={flow} day={day} />
      </Card>
      <Card title={t("tasks.flow.cfd.title")} lead={t("tasks.flow.cfd.lead")}>
        <CfdChart flow={flow} day={day} />
      </Card>
      <Card title={t("tasks.flow.aging.title")} lead={t("tasks.flow.aging.lead")}>
        {flow.aging.length === 0 ? (
          <p className="m-0 text-ink-3">{t("tasks.flow.aging.none")}</p>
        ) : (
          <table className="w-full table-fixed border-collapse text-[13px]">
            <colgroup>
              <col />
              <col className="w-28" />
              <col className="w-18" />
            </colgroup>
            <thead className="sr-only">
              <tr>
                <th>{t("tasks.flow.aging.task")}</th>
                <th>{t("tasks.flow.aging.status")}</th>
                <th>{t("tasks.flow.aging.days")}</th>
              </tr>
            </thead>
            <tbody>
              {flow.aging.map((a) => (
                <tr key={a.task_id} className="border-t border-line first:border-t-0">
                  <td className="py-1.5 pr-2">
                    <Link
                      to="/tasks/$taskId"
                      params={{ taskId: a.task_id }}
                      search={{ ...search, layout: "board" }}
                      className="flex min-w-0 items-center gap-1.5 text-ink no-underline hover:underline"
                    >
                      <span className="shrink-0 font-mono text-ink-3">{t("tasks.number", { number: a.number })}</span>
                      {a.blocked ? <Lock aria-label={t("tasks.blocked.label")} className="size-3.5 shrink-0 text-lamp" /> : null}
                      <span className="truncate">{a.title}</span>
                    </Link>
                  </td>
                  <td className="truncate py-1.5 pr-2 text-ink-2">{t(`tasks.board.column.${a.status}`)}</td>
                  <td className="py-1.5 text-right whitespace-nowrap tabular-nums text-ink">{days(a.days)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Card>
    </div>
  );
}

function Card({ title, lead, children }: { title: string; lead: string; children: ReactNode }) {
  const id = useId();
  return (
    <section aria-labelledby={id} className="flex min-w-0 flex-col gap-2 rounded-xl border border-line bg-surface p-4">
      <h2 id={id} className="m-0 text-[14px] font-semibold">
        {title}
      </h2>
      <p className="m-0 text-[12.5px] text-ink-2">{lead}</p>
      {children}
    </section>
  );
}

/** A round top for the axis: 1, 2, 5 × 10ⁿ; counts get an even top, so the middle tick is whole. */
function niceMax(v: number, counts = false): number {
  if (counts && v <= 10) return Math.max(2, Math.ceil(v / 2) * 2);
  if (v <= 0) return 1;
  const p = 10 ** Math.floor(Math.log10(v));
  return ([1, 2, 5, 10].find((m) => m * p >= v) ?? 10) * p;
}

/** Recessive grid and the y axis with three ticks. */
function Frame({ w, max, format = String }: { w: number; max: number; format?: (v: number) => string }) {
  const y = (v: number) => PAD.top + (H - PAD.top - PAD.bottom) * (1 - v / max);
  return (
    <g>
      {[0, max / 2, max].map((v) => (
        <g key={v}>
          <line x1={PAD.left} x2={w - PAD.right} y1={y(v)} y2={y(v)} stroke="var(--color-line)" strokeWidth={1} />
          <text x={PAD.left - 6} y={y(v)} dy="0.32em" textAnchor="end" fontSize={11} fill="var(--color-ink-3)">
            {format(v)}
          </text>
        </g>
      ))}
    </g>
  );
}

function XLabels({ labels }: { labels: { x: number; text: string }[] }) {
  return (
    <g>
      {labels.map((l, i) => (
        <text
          key={i}
          x={l.x}
          y={H - 6}
          fontSize={11}
          fill="var(--color-ink-3)"
          textAnchor={i === 0 ? "start" : i === labels.length - 1 ? "end" : "middle"}
        >
          {l.text}
        </text>
      ))}
    </g>
  );
}

/** Draws at the card's own width, so labels keep their size on a phone. */
function Chart({ label, children }: { label: string; children: (w: number) => ReactNode }) {
  const ref = useRef<HTMLDivElement>(null);
  const [w, setW] = useState(600);
  useEffect(() => {
    const el = ref.current;
    if (!el || typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(([entry]) => entry && setW(Math.max(240, Math.round(entry.contentRect.width))));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);
  return (
    <div ref={ref} className="min-w-0">
      <svg viewBox={`0 0 ${w} ${H}`} width={w} height={H} role="img" aria-label={label} className="block max-w-full overflow-visible">
        {children(w)}
      </svg>
    </div>
  );
}

function CycleChart({ flow, day, days }: { flow: TaskFlow; day: (iso: string) => string; days: (n: number) => string }) {
  const { t } = useTranslation();
  const start = Date.parse(`${flow.since}T00:00:00Z`);
  const end = Date.parse(`${flow.until}T00:00:00Z`) + 86_400_000;
  const max = niceMax(Math.max(1, ...flow.cycle.map((c) => c.days)));
  const y = (v: number) => PAD.top + (H - PAD.top - PAD.bottom) * (1 - v / max);
  return (
    <Chart label={t("tasks.flow.cycle.title")}>
      {(w) => {
        const x = (iso: string) => PAD.left + ((Date.parse(iso) - start) / (end - start)) * (w - PAD.left - PAD.right);
        const line = (v: number | null, label: string) =>
          v === null ? null : (
            <g>
              <line x1={PAD.left} x2={w - PAD.right} y1={y(v)} y2={y(v)} stroke="var(--color-ink-3)" strokeWidth={1} strokeDasharray="4 4" />
              <text x={w - PAD.right} y={y(v) - 4} textAnchor="end" fontSize={11} fill="var(--color-ink-2)">
                {label}
              </text>
            </g>
          );
        return (
          <>
            <Frame w={w} max={max} />
            {line(flow.cycle_p85, `85% · ${flow.cycle_p85 === null ? "" : days(flow.cycle_p85)}`)}
            {line(flow.cycle_p50, `50% · ${flow.cycle_p50 === null ? "" : days(flow.cycle_p50)}`)}
            {flow.cycle.map((c) => (
              <circle key={c.task_id} cx={x(c.done_at)} cy={y(c.days)} r={4.5} fill="var(--color-chart-1)" stroke="var(--color-surface)" strokeWidth={2}>
                <title>{`${t("tasks.number", { number: c.number })} ${c.title} — ${days(c.days)}`}</title>
              </circle>
            ))}
            <XLabels
              labels={[
                { x: PAD.left, text: day(flow.since) },
                { x: w - PAD.right, text: day(flow.until) },
              ]}
            />
          </>
        );
      }}
    </Chart>
  );
}

function ThroughputChart({ flow, day }: { flow: TaskFlow; day: (iso: string) => string }) {
  const { t } = useTranslation();
  const weeks = flow.throughput;
  const max = niceMax(Math.max(...weeks.map((wk) => wk.done), 0), true);
  const y = (v: number) => PAD.top + (H - PAD.top - PAD.bottom) * (1 - v / max);
  const base = y(0);
  return (
    <Chart label={t("tasks.flow.throughput.title")}>
      {(w) => {
        const slot = (w - PAD.left - PAD.right) / Math.max(1, weeks.length);
        const bar = Math.max(6, Math.min(24, slot - 6));
        const first = weeks[0];
        const last = weeks[weeks.length - 1];
        return (
          <>
            <Frame w={w} max={max} />
            {weeks.map((wk, i) => {
              const cx = PAD.left + slot * i + slot / 2;
              const h = base - y(wk.done);
              return (
                <g key={wk.week}>
                  <title>{t("tasks.flow.throughput.week", { week: day(wk.week), count: wk.done })}</title>
                  {/* The hit target is the whole slot; the bar is rounded at its data end. */}
                  <rect x={cx - slot / 2} y={PAD.top} width={slot} height={base - PAD.top} fill="transparent" />
                  {wk.done > 0 ? (
                    <path d={`M${cx - bar / 2},${base} v${-(h - 4)} q0,-4 4,-4 h${bar - 8} q4,0 4,4 v${h - 4} z`} fill="var(--color-chart-1)" />
                  ) : null}
                </g>
              );
            })}
            {first && last ? (
              <XLabels
                labels={
                  first === last
                    ? [{ x: PAD.left + slot / 2, text: day(first.week) }]
                    : [
                        { x: PAD.left, text: day(first.week) },
                        { x: w - PAD.right, text: day(last.week) },
                      ]
                }
              />
            ) : null}
          </>
        );
      }}
    </Chart>
  );
}

function CfdChart({ flow, day }: { flow: TaskFlow; day: (iso: string) => string }) {
  const { t } = useTranslation();
  const rows = flow.cfd;
  const totals = rows.map((d) => BANDS.reduce((n, b) => n + d[b.key], 0));
  const max = niceMax(Math.max(...totals, 0), true);
  const y = (v: number) => PAD.top + (H - PAD.top - PAD.bottom) * (1 - v / max);
  const last = rows[rows.length - 1];
  return (
    <>
      <Chart label={t("tasks.flow.cfd.title")}>
        {(w) => {
          const step = (w - PAD.left - PAD.right) / Math.max(1, rows.length - 1);
          const x = (i: number) => PAD.left + step * i;
          let below = rows.map(() => 0);
          const areas = BANDS.map((b) => {
            const top = rows.map((d, i) => below[i]! + d[b.key]);
            const upper = top.map((v, i) => `${x(i)},${y(v)}`).join(" L");
            const lower = below
              .map((v, i) => `${x(i)},${y(v)}`)
              .reverse()
              .join(" L");
            below = top;
            return { ...b, d: `M${upper} L${lower} Z` };
          });
          return (
            <>
              <Frame w={w} max={max} />
              {areas.map((a) => (
                // A surface line between the bands keeps them apart.
                <path key={a.key} d={a.d} fill={a.color} stroke="var(--color-surface)" strokeWidth={1} />
              ))}
              {rows.map((d, i) => (
                <rect key={d.day} x={x(i) - step / 2} y={PAD.top} width={step} height={H - PAD.top - PAD.bottom} fill="transparent">
                  <title>{[day(d.day), ...BANDS.map((b) => `${t(`tasks.board.column.${b.key}`)}: ${d[b.key]}`)].join("\n")}</title>
                </rect>
              ))}
              <XLabels
                labels={[
                  { x: PAD.left, text: day(flow.since) },
                  { x: w - PAD.right, text: day(flow.until) },
                ]}
              />
            </>
          );
        }}
      </Chart>
      {/* The legend names every band with today's count, so colour is never alone. */}
      <ul className="m-0 flex list-none flex-wrap gap-x-4 gap-y-1 p-0 text-[12.5px] text-ink-2">
        {[...BANDS].reverse().map((b) => (
          <li key={b.key} className="flex items-center gap-1.5">
            <span aria-hidden="true" className="size-2.5 rounded-sm" style={{ background: b.color }} />
            {t(`tasks.board.column.${b.key}`)}
            <span className="tabular-nums text-ink">{last ? last[b.key] : 0}</span>
          </li>
        ))}
      </ul>
    </>
  );
}
