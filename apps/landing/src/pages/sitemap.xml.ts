// sitemap.xml with hreflang alternates, generated at build time without @astrojs/sitemap.
// When the site grows (docs, /vs/*), switch to @astrojs/sitemap (needs owner approval, 00 §16).
import type { APIRoute } from "astro";
import { LOCALES } from "../i18n";
import { PAGES, SITE_URL } from "../site";

const abs = (path: string) => new URL(path, SITE_URL).href;

export const GET: APIRoute = () => {
  const alternates = [
    ...LOCALES.map((l) => `    <xhtml:link rel="alternate" hreflang="${l}" href="${abs(PAGES[l])}"/>`),
    `    <xhtml:link rel="alternate" hreflang="x-default" href="${abs(PAGES.en)}"/>`,
  ].join("\n");
  const urls = LOCALES.map((l) => `  <url>\n    <loc>${abs(PAGES[l])}</loc>\n${alternates}\n  </url>`).join("\n");
  const body = `<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9" xmlns:xhtml="http://www.w3.org/1999/xhtml">
${urls}
</urlset>
`;
  return new Response(body, { headers: { "Content-Type": "application/xml; charset=utf-8" } });
};
