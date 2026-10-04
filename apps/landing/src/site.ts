// Site-wide constants shared by both locales.

export const SITE_URL = "https://tolkovo.dev";
export const REPO_URL = "https://github.com/tolkovo/tolkovo";
export const COMMUNITY_CHAT_URL = "https://t.me/tolkovo_chat";

/**
 * Waitlist endpoint of the product API. The landing is static hosting and the
 * API runs on our EU server under another origin, set at build time through
 * PUBLIC_API_ORIGIN; without it the path stays relative (local preview).
 */
const API_ORIGIN = (import.meta.env.PUBLIC_API_ORIGIN ?? "").replace(/\/$/, "");
export const WAITLIST_ENDPOINT = `${API_ORIGIN}/api/v1/waitlist`;

export const PAGES = {
  ru: "/",
  en: "/en/",
} as const;
