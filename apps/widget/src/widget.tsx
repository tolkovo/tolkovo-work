import { render } from "preact";

import { Controller } from "./controller";
import { localized, resolveLocale, translator } from "./i18n";
import type { SocketFactory } from "./realtime";
import styles from "./styles.css?inline";
import type { Options, Theme } from "./types";
import { App, type View } from "./ui/app";

/**
 * One widget on the page: a host element with an open shadow root (docs/04 §6.1), so the page's
 * styles do not leak in and the widget's do not leak out. The theme becomes --tk-* custom
 * properties on the host; values the browser does not accept are ignored, so a typo in the host
 * app's code cannot break the layout.
 */
export class Widget {
  readonly controller: Controller;
  private readonly host: HTMLElement;
  private readonly root: ShadowRoot;
  private readonly onVisibility = () => this.controller.onVisible();

  constructor(options: Options, socket?: SocketFactory) {
    if (!options.widgetKey || !options.apiBase) throw new Error("Tolkovo.init: widgetKey and apiBase are required");
    this.controller = new Controller({
      widgetKey: options.widgetKey,
      apiBase: options.apiBase,
      user: options.user,
      storage: options.storage,
      open: options.open,
      launcher: options.launcher,
      ...(socket ? { socket } : {}),
    });
    this.host = document.createElement("tolkovo-widget");
    this.host.setAttribute("data-mode", options.theme?.mode ?? "auto");
    applyTheme(this.host, options.theme);
    this.root = this.host.attachShadow({ mode: "open" });
    adoptStyles(this.root, styles);
    const mount = document.createElement("div");
    this.root.appendChild(mount);
    (options.container ?? document.body).appendChild(this.host);

    const locale = resolveLocale(options.locale);
    const view: View = {
      locale,
      t: translator(locale),
      title: localized(options.title, locale),
      subtitle: localized(options.subtitle, locale),
      greeting: localized(options.greeting, locale),
      placeholder: localized(options.placeholder, locale),
      logo: options.logo,
      position: options.theme?.position === "bottom-left" ? "bottom-left" : "bottom-right",
    };
    render(<App c={this.controller} view={view} />, mount);
    document.addEventListener("visibilitychange", this.onVisibility);
    void this.controller.start();
  }

  /** The shadow root, for tests and for apps that style around the widget. */
  get shadowRoot(): ShadowRoot {
    return this.root;
  }

  destroy(): void {
    document.removeEventListener("visibilitychange", this.onVisibility);
    this.controller.destroy();
    render(null, this.root.lastChild as Element);
    this.host.remove();
  }
}

function adoptStyles(root: ShadowRoot, css: string): void {
  try {
    const sheet = new CSSStyleSheet();
    sheet.replaceSync(css);
    root.adoptedStyleSheets = [sheet];
  } catch {
    // Older engines: a <style> element. A page CSP without 'unsafe-inline' for styles blocks it,
    // which is why adoptedStyleSheets comes first.
    const style = document.createElement("style");
    style.textContent = css;
    root.appendChild(style);
  }
}

/** Sets a custom property only to a value the browser accepts for that kind of property. */
function setChecked(el: HTMLElement, name: string, value: string | undefined, kind: "color" | "length" | "font-family" | "z-index"): void {
  if (value === undefined || value === "") return;
  const probe = kind === "length" ? "width" : kind;
  if (typeof CSS !== "undefined" && typeof CSS.supports === "function" && !CSS.supports(probe, value)) return;
  el.style.setProperty(name, value);
}

function px(n: number | undefined): string | undefined {
  return typeof n === "number" && Number.isFinite(n) && n >= 0 && n <= 200 ? `${n}px` : undefined;
}

export function applyTheme(el: HTMLElement, theme: Theme | undefined): void {
  if (!theme) return;
  setChecked(el, "--tk-accent", theme.accent, "color");
  if (theme.accentText) setChecked(el, "--tk-on-accent", theme.accentText, "color");
  else if (theme.accent) {
    const dark = isDark(theme.accent);
    if (dark !== null) el.style.setProperty("--tk-on-accent", dark ? "var(--tk-on-dark)" : "var(--tk-on-light)");
  }
  setChecked(el, "--tk-radius", px(theme.radius), "length");
  setChecked(el, "--tk-offset-x", px(theme.offsetX), "length");
  setChecked(el, "--tk-offset-y", px(theme.offsetY), "length");
  setChecked(el, "--tk-font", theme.fontFamily, "font-family");
  if (typeof theme.zIndex === "number" && Number.isInteger(theme.zIndex)) setChecked(el, "--tk-z", String(theme.zIndex), "z-index");
}

/** Whether a hex color needs light text (WCAG relative luminance); null for other forms. */
export function isDark(color: string): boolean | null {
  const m = /^#?([0-9a-f]{3}|[0-9a-f]{6})$/i.exec(color.trim());
  if (!m) return null;
  let hex = m[1]!;
  if (hex.length === 3) hex = [...hex].map((ch) => ch + ch).join("");
  const channel = (i: number) => {
    const v = parseInt(hex.slice(i, i + 2), 16) / 255;
    return v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4;
  };
  const l = 0.2126 * channel(0) + 0.7152 * channel(2) + 0.0722 * channel(4);
  // Contrast with white beats contrast with near-black below this luminance.
  return l < 0.179;
}
