import { defineConfig } from "vite";

/**
 * The widget is a library (docs/04 §6.1). The default build is the ES module for npm (sites and
 * Electron apps bundle it); `--mode script` adds an IIFE for a plain <script> that defines
 * window.Tolkovo. Preact is bundled in either way, so the widget loads nothing at runtime.
 */
export default defineConfig(({ mode }) => ({
  build: {
    target: "es2020",
    lib:
      mode === "script"
        ? { entry: "src/global.ts", name: "TolkovoWidget", formats: ["iife"], fileName: () => "tolkovo-widget.iife.js" }
        : { entry: "src/index.ts", formats: ["es"], fileName: () => "index.js" },
    sourcemap: true,
    emptyOutDir: mode !== "script",
  },
}));
