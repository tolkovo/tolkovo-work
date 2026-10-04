/**
 * Browser and OS from a User-Agent string, for the session list ("Firefox · macOS"). Names are
 * product names and stay untranslated. Only common browsers are recognised; anything else is
 * null and the screen says "unknown device". Order matters: Edge, Opera and Yandex Browser
 * also claim to be Chrome, and Chrome claims to be Safari.
 */
const browsers: readonly [RegExp, string][] = [
  [/\bEdgA?\//, "Edge"],
  [/\b(?:OPR|Opera)\//, "Opera"],
  [/\bYaBrowser\//, "Yandex Browser"],
  [/\bSamsungBrowser\//, "Samsung Internet"],
  [/\b(?:Firefox|FxiOS)\//, "Firefox"],
  [/\b(?:Chrome|CriOS|Chromium)\//, "Chrome"],
  [/\bVersion\/[\d.]+.*\bSafari\//, "Safari"],
];

const systems: readonly [RegExp, string][] = [
  [/\bWindows\b/, "Windows"],
  [/\bAndroid\b/, "Android"],
  [/\biPad\b/, "iPadOS"],
  [/\b(?:iPhone|iPod)\b/, "iOS"],
  [/\bCrOS\b/, "ChromeOS"],
  [/\bMac OS X\b|\bMacintosh\b/, "macOS"],
  [/\bLinux\b/, "Linux"],
];

export function describeUserAgent(ua: string): { browser: string | null; os: string | null } {
  const find = (rules: readonly [RegExp, string][]) => rules.find(([re]) => re.test(ua))?.[1] ?? null;
  return { browser: find(browsers), os: find(systems) };
}

/** "Firefox · macOS", "Chrome", or null when nothing is recognised. */
export function deviceLabel(ua: string): string | null {
  const { browser, os } = describeUserAgent(ua);
  return [browser, os].filter(Boolean).join(" · ") || null;
}
