// Bundle budget (docs/00 §7): everything the browser loads before the first screen, that is the
// entry script and the chunks dist/index.html preloads. Lazy route chunks are not counted.
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

const html = readFileSync(fileURLToPath(import.meta.resolve("./dist/index.html")), "utf8");
const initial = [...html.matchAll(/<(?:script[^>]+src|link[^>]+rel="modulepreload"[^>]+href)="\/([^"]+\.js)"/g)]
  .map((m) => m[1])
  .filter((path) => path.startsWith("assets/"));

export default [{ name: "initial JS (entry + preloaded chunks)", path: initial.map((p) => `dist/${p}`), limit: "250 kB", gzip: true }];
