import tailwindcss from "@tailwindcss/vite";
import { tanstackRouter } from "@tanstack/router-plugin/vite";
import react from "@vitejs/plugin-react";
import { defineConfig, type Plugin } from "vite";

/**
 * Content Security Policy for the built panel (docs/00 §9.6): no 'unsafe-inline'.
 * Only added to the production build: the dev server injects an inline React Refresh preamble.
 * The server (or reverse proxy) should send the same policy as a header, plus frame-ancestors,
 * which a <meta> tag cannot carry.
 */
export const contentSecurityPolicy = [
  "default-src 'self'",
  "script-src 'self'",
  "style-src 'self'",
  "img-src 'self' data:",
  "font-src 'self'",
  "connect-src 'self'",
  "object-src 'none'",
  "base-uri 'self'",
  "form-action 'self'",
].join("; ");

function cspMeta(): Plugin {
  return {
    name: "tolkovo:csp-meta",
    apply: "build",
    transformIndexHtml: () => [
      {
        tag: "meta",
        attrs: { "http-equiv": "Content-Security-Policy", content: contentSecurityPolicy },
        injectTo: "head-prepend",
      },
    ],
  };
}

export default defineConfig({
  plugins: [
    // Must come before the React plugin: it generates src/routeTree.gen.ts and splits route chunks.
    tanstackRouter({
      target: "react",
      autoCodeSplitting: true,
      quoteStyle: "double",
      routeFileIgnorePattern: String.raw`\.test\.tsx?$`,
    }),
    react(),
    tailwindcss(),
    cspMeta(),
  ],
  server: {
    port: 5173,
    proxy: {
      "/api": { target: "http://localhost:8080", changeOrigin: false, ws: true },
    },
  },
  preview: {
    port: 4173,
  },
  build: {
    target: "es2022",
    sourcemap: true,
  },
});
