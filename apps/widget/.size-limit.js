// Bundle budget (docs/04 §6.1): the whole widget, Preact included, at most 35 kB gzip.
export default [
  { name: "widget (ES module)", path: "dist/index.js", limit: "35 kB", gzip: true },
  { name: "widget (script tag)", path: "dist/tolkovo-widget.iife.js", limit: "35 kB", gzip: true },
];
