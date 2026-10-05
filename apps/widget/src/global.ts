/**
 * The <script> build: window.Tolkovo is the widget's API, and calls queued before the script
 * loaded (the snippet's stub, docs/04 §6.2: Tolkovo("init", {...})) run now.
 */
import { Tolkovo } from "./index";

type Queued = { q?: unknown[][] };
const w = window as unknown as { Tolkovo?: Queued };
const queued = typeof w.Tolkovo === "function" || typeof w.Tolkovo === "object" ? (w.Tolkovo?.q ?? []) : [];
(window as unknown as { Tolkovo: typeof Tolkovo }).Tolkovo = Tolkovo;
for (const [method, ...args] of queued) {
  const fn = (Tolkovo as unknown as Record<string, unknown>)[String(method)];
  if (typeof fn === "function") (fn as (...a: unknown[]) => unknown)(...args);
}
