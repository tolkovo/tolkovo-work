// Switchboard demo: draws cords between inputs, the AI node and the lines, then loops
// through example events. Purely decorative: the static markup already tells the story,
// so with prefers-reduced-motion only the cords are drawn.
import type { BoardEvent } from "../i18n/types";

const NS = "http://www.w3.org/2000/svg";

type Point = { x: number; y: number };

function parseJSON<T>(raw: string | undefined, isValid: (v: unknown) => v is T): T | null {
  if (!raw) return null;
  try {
    const value: unknown = JSON.parse(raw);
    return isValid(value) ? value : null;
  } catch {
    return null;
  }
}

function isEvents(v: unknown): v is BoardEvent[] {
  return (
    Array.isArray(v) &&
    v.every(
      (e: unknown) =>
        typeof e === "object" &&
        e !== null &&
        typeof (e as BoardEvent).from === "string" &&
        typeof (e as BoardEvent).to === "string" &&
        typeof (e as BoardEvent).who === "string" &&
        typeof (e as BoardEvent).note === "string",
    )
  );
}

function isLabels(v: unknown): v is Record<string, string> {
  return typeof v === "object" && v !== null && Object.values(v).every((x) => typeof x === "string");
}

function init(board: HTMLElement): void {
  const svg = board.querySelector<SVGSVGElement>("#cords");
  const ai = board.querySelector<HTMLElement>("#ai");
  const ticker = board.querySelector<HTMLElement>("#ticker");
  const events = parseJSON(board.dataset["events"], isEvents);
  const labels = parseJSON(board.dataset["channels"], isLabels);
  if (!svg || !ai || !ticker || !events || events.length === 0 || !labels) return;

  const reduce = matchMedia("(prefers-reduced-motion: reduce)").matches;
  let paths: Record<string, SVGPathElement> = {};

  const rel = (el: Element) => {
    const b = board.getBoundingClientRect();
    const r = el.getBoundingClientRect();
    return { l: r.left - b.left, r: r.right - b.left, y: r.top - b.top + r.height / 2 };
  };
  const curve = (a: Point, b: Point) => {
    const dx = (b.x - a.x) * 0.5;
    return `M${a.x},${a.y} C${a.x + dx},${a.y} ${b.x - dx},${b.y} ${b.x},${b.y}`;
  };
  const mk = (id: string, d: string) => {
    const p = document.createElementNS(NS, "path");
    p.setAttribute("d", d);
    p.setAttribute("class", "cord");
    svg.appendChild(p);
    paths[id] = p;
  };
  const draw = () => {
    svg.replaceChildren();
    paths = {};
    if (getComputedStyle(svg).display === "none") return;
    const A = rel(ai);
    board.querySelectorAll<HTMLElement>(".jack").forEach((j) => {
      const p = rel(j);
      mk(`in-${j.dataset["id"]}`, curve({ x: p.r, y: p.y }, { x: A.l, y: A.y }));
    });
    board.querySelectorAll<HTMLElement>(".linebox").forEach((l) => {
      const p = rel(l);
      mk(`out-${l.dataset["id"]}`, curve({ x: A.r, y: A.y }, { x: p.l, y: p.y }));
    });
  };

  const wait = (ms: number) => new Promise<void>((r) => setTimeout(r, ms));
  const pulse = (id: string, human: boolean, ms: number) =>
    new Promise<void>((res) => {
      const p = paths[id];
      if (!p) {
        res();
        return;
      }
      p.classList.add("live");
      const c = document.createElementNS(NS, "circle");
      c.setAttribute("r", "5");
      c.setAttribute("class", human ? "pulse human" : "pulse");
      svg.appendChild(c);
      const len = p.getTotalLength();
      const t0 = performance.now();
      const step = (now: number) => {
        const k = Math.min(1, (now - t0) / ms);
        const pt = p.getPointAtLength(len * k);
        c.setAttribute("cx", String(pt.x));
        c.setAttribute("cy", String(pt.y));
        if (k < 1) requestAnimationFrame(step);
        else {
          c.remove();
          p.classList.remove("live");
          res();
        }
      };
      requestAnimationFrame(step);
    });

  const loads = new Map<string, { el: HTMLElement; load: number; cap: number }>();
  board.querySelectorAll<HTMLElement>(".load").forEach((el) => {
    const name = el.dataset["agent"];
    if (name) loads.set(name, { el, load: Number(el.dataset["load"]), cap: Number(el.dataset["cap"]) });
  });
  const setLoad = (name: string, delta: number) => {
    const a = loads.get(name);
    if (!a) return;
    a.load = Math.max(0, Math.min(a.cap, a.load + delta));
    a.el.textContent = `${a.load}/${a.cap}`;
  };
  const counter = (id: string) => {
    const el = board.querySelector<HTMLElement>(id);
    return () => {
      if (el) el.textContent = String(Number(el.textContent) + 1);
    };
  };
  const incAi = counter("#kAi");
  const incHuman = counter("#kH");

  let minute = 32;
  const tick = (kind: "a" | "h", text: string) => {
    minute++;
    const row = document.createElement("li");
    const time = document.createElement("time");
    time.textContent = `14:${String(minute % 60).padStart(2, "0")}`;
    const span = document.createElement("span");
    span.className = kind;
    span.textContent = text;
    row.append(time, span);
    ticker.appendChild(row);
    while (ticker.children.length > 4) ticker.firstElementChild?.remove();
  };

  // Stops when the board is off screen, so the loop costs nothing while the user reads below.
  let visible = true;
  new IntersectionObserver((entries) => {
    visible = entries.some((e) => e.isIntersecting);
  }).observe(board);

  const run = async () => {
    for (let i = 0; ; i++) {
      if (!visible || document.hidden) {
        await wait(1000);
        i--;
        continue;
      }
      const e = events[i % events.length];
      if (!e) continue;
      const jack = board.querySelector(`.jack[data-id="${e.from}"]`);
      jack?.classList.add("hot");
      await pulse(`in-${e.from}`, false, 900);
      jack?.classList.remove("hot");
      const channel = labels[e.from] ?? e.from;
      if (e.to === "ai") {
        await wait(500);
        incAi();
        tick("a", `${e.who} · ${channel} → ${e.note}`);
      } else {
        const lamp = board.querySelector<HTMLElement>(`#lamp${e.to}`);
        await pulse(`out-${e.to}`, true, 900);
        incHuman();
        if (lamp) lamp.className = "lamp on";
        await wait(1400);
        if (lamp) lamp.className = "lamp taken";
        if (e.agent) setLoad(e.agent, 1);
        tick("h", `${e.who} · ${channel} → ${e.to} · ${e.agent ?? ""} · ${e.note}`);
        await wait(900);
        if (lamp) lamp.className = "lamp";
        // Someone closed a conversation: load goes down for a random busy operator.
        const busy = [...loads.entries()].filter(([, a]) => a.load > 0).map(([n]) => n);
        const n = busy[Math.floor(Math.random() * busy.length)];
        if (n) setLoad(n, -1);
      }
      await wait(1200);
    }
  };

  draw();
  addEventListener("resize", draw);
  void document.fonts?.ready.then(draw);
  if (!reduce) setTimeout(() => void run(), 800);
}

const board = document.getElementById("board");
if (board) init(board);
