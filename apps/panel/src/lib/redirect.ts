/** Where the panel goes after sign-in when there is no (valid) return path. */
export const HOME_PATH = "/inbox";

const PROBE_ORIGIN = "http://tolkovo.invalid";

/**
 * Returns `value` if it is a path inside the panel, otherwise null. The return path comes from
 * the URL (`/login?redirect=…`), i.e. from anyone who can send a link: anything that could leave
 * the origin ("//evil", "/\evil", "https://…", "javascript:…") is an open redirect and is dropped.
 */
export function safeReturnPath(value: unknown): string | null {
  if (typeof value !== "string" || !value.startsWith("/") || value.startsWith("//")) return null;
  if (/[\\\s]/.test(value)) return null;
  let url: URL;
  try {
    url = new URL(value, PROBE_ORIGIN);
  } catch {
    return null;
  }
  if (url.origin !== PROBE_ORIGIN) return null;
  // Returning to a sign-in screen would loop.
  if (url.pathname === "/login" || url.pathname === "/register") return null;
  return url.pathname + url.search + url.hash;
}

/**
 * `/login` search params: the optional return path, validated (URL is external input).
 * The key is always present: the router merges the raw search of parent routes into this
 * result, so omitting it would let an unsafe raw value through.
 */
export function validateLoginSearch(search: Record<string, unknown>): { redirect?: string | undefined } {
  return { redirect: safeReturnPath(search.redirect) ?? undefined };
}
