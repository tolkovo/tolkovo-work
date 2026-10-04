// Static landing (docs/05-landing.md, backlog L-01).
// RU lives at the root, EN under /en/ (05 §7.2).
import { defineConfig } from "astro/config";

export default defineConfig({
  site: "https://tolkovo.dev",
  output: "static",
  trailingSlash: "always",
  build: {
    format: "directory",
    // One small stylesheet: inlining it removes a render-blocking request (LCP budget, 00 §7.6).
    inlineStylesheets: "always",
  },
  // The toolbar loads extra client code in dev and is not needed here.
  devToolbar: { enabled: false },
});
