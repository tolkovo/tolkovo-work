/**
 * @tolkovo/widget — the Tolkovo chat widget for sites and Electron apps (docs/04 §6).
 *
 *   import { Tolkovo } from "@tolkovo/widget";
 *   Tolkovo.init({ widgetKey: "tk_widget_…", apiBase: "https://support.example.com" });
 *
 * Calls before init are ignored; a second init replaces the widget.
 */
import type { EventName, Handler, Options, TolkovoApi, User } from "./types";
import { Widget } from "./widget";

export type { ChatMessage, EventName, Events, Handler, Locale, Localized, Options, Theme, TokenStorage, TolkovoApi, User } from "./types";

let current: Widget | null = null;
/** Handlers given before init are kept for the widget init creates. */
const early: [EventName, Handler<EventName>][] = [];

export const Tolkovo: TolkovoApi = {
  init(options: Options) {
    current?.destroy();
    current = new Widget(options);
    for (const [event, handler] of early) current.controller.on(event, handler);
  },
  identify: (user: User) => current?.controller.identify(user) ?? Promise.resolve(),
  logout: () => current?.controller.logout() ?? Promise.resolve(),
  open: () => current?.controller.setOpen(true),
  close: () => current?.controller.setOpen(false),
  toggle: () => current?.controller.setOpen(!current.controller.get().open),
  show: () => current?.controller.setLauncher(true),
  hide: () => current?.controller.setLauncher(false),
  on(event, handler) {
    early.push([event, handler as Handler<EventName>]);
    current?.controller.on(event, handler);
  },
  off(event, handler) {
    const i = early.findIndex(([e, h]) => e === event && h === handler);
    if (i >= 0) early.splice(i, 1);
    current?.controller.off(event, handler);
  },
  destroy() {
    current?.destroy();
    current = null;
  },
};
