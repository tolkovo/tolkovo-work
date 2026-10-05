import type { Locale } from "./types";

/**
 * What a visitor may attach (api/openapi.yaml, POST /widget/uploads): the server checks again and
 * looks inside each file, this only spares a round trip and explains the refusal at once.
 */

export const MAX_FILE_SIZE = 10 * 1024 * 1024;
export const MAX_FILES = 5;

const TYPES = new Set([
  "image/png",
  "image/jpeg",
  "image/gif",
  "image/webp",
  "application/pdf",
  "text/plain",
  "text/csv",
  "application/json",
  "application/zip",
  "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
  "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
]);

/** Browsers leave the type of logs and some text files empty: the extension tells. */
const BY_EXTENSION: Record<string, string> = {
  log: "text/plain",
  txt: "text/plain",
  csv: "text/csv",
  json: "application/json",
  zip: "application/zip",
  jpg: "image/jpeg",
  jpeg: "image/jpeg",
  png: "image/png",
  gif: "image/gif",
  webp: "image/webp",
  pdf: "application/pdf",
};

/** The `accept` of the file picker. */
export const ACCEPT = [...TYPES, ".log", ".txt", ".csv", ".json"].join(",");

/** The type to declare for a file, or null when it is not accepted. */
export function fileType(file: File): string | null {
  const declared = file.type.split(";")[0]!.trim().toLowerCase();
  if (TYPES.has(declared)) return declared;
  if (declared && declared !== "application/octet-stream" && !declared.startsWith("text/")) return null;
  const ext = file.name.toLowerCase().split(".").pop() ?? "";
  return BY_EXTENSION[ext] ?? null;
}

export const isImage = (contentType: string) => contentType.startsWith("image/");

/** 93 Б, 340 КБ, 1,2 МБ, as the panel shows sizes. */
export function formatSize(bytes: number, locale: Locale): string {
  const ru = locale === "ru";
  const kb = bytes / 1024;
  const [value, unit] =
    bytes < 1024 ? [bytes, ru ? "Б" : "B"] : kb < 1024 ? [Math.round(kb), ru ? "КБ" : "KB"] : [kb / 1024, ru ? "МБ" : "MB"];
  return `${new Intl.NumberFormat(locale, { maximumFractionDigits: 1 }).format(value)} ${unit}`;
}
