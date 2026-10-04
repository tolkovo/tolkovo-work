import type { LoginRequest, RegisterRequest, UpdateWorkspaceRequest } from "@tolkovo/api-client";
import { z } from "zod";

/**
 * Client-side checks that mirror the constraints of api/openapi.yaml, so people see mistakes
 * before a round trip. The server validates again. Messages are keys of `validation.*`.
 * `satisfies` ties every form to the generated request type.
 */
export const validationKeys = ["required", "email", "passwordLength", "slug", "tooLong"] as const;
export type ValidationKey = (typeof validationKeys)[number];

const required = (max: number) => z.string().trim().min(1, "required").max(max, "tooLong");

const email = z
  .string()
  .trim()
  .min(1, "required")
  .max(254, "tooLong")
  // The server is the authority on addresses; this only catches obvious typos.
  .regex(/^[^\s@]+@[^\s@]+\.[^\s@]+$/, "email");

export const SLUG_PATTERN = /^[a-z0-9][a-z0-9-]{1,38}[a-z0-9]$/;

export const loginSchema = z.object({
  email,
  password: z.string().min(1, "required").max(128, "tooLong"),
}) satisfies z.ZodType<LoginRequest>;

export const registerSchema = z.object({
  name: required(120),
  workspace_name: required(120),
  workspace_slug: z.string().trim().min(1, "required").regex(SLUG_PATTERN, "slug"),
  email,
  password: z.string().min(1, "required").min(8, "passwordLength").max(128, "passwordLength"),
}) satisfies z.ZodType<Omit<RegisterRequest, "locale" | "timezone">>;

export const workspaceSchema = z.object({
  name: required(120),
  locale: z.enum(["ru", "en"]),
  timezone: z.string().min(1, "required").max(64, "tooLong"),
}) satisfies z.ZodType<UpdateWorkspaceRequest>;

export type FieldErrors<F extends string> = Partial<Record<F, ValidationKey>>;

type Validated<T, F extends string> = { ok: true; data: T } | { ok: false; errors: FieldErrors<F> };

/** Validates form values; on failure returns the first problem of each field, in field order. */
export function validateForm<S extends z.ZodObject>(
  schema: S,
  values: Record<string, unknown>,
): Validated<z.output<S>, Extract<keyof z.output<S>, string>> {
  type F = Extract<keyof z.output<S>, string>;
  const parsed = schema.safeParse(values);
  if (parsed.success) return { ok: true, data: parsed.data };
  const errors: FieldErrors<F> = {};
  for (const issue of parsed.error.issues) {
    const field = issue.path[0];
    if (typeof field !== "string" || field in errors) continue;
    const key = (validationKeys as readonly string[]).includes(issue.message) ? issue.message : "required";
    errors[field as F] = key as ValidationKey;
  }
  return { ok: false, errors };
}

/** String values of a submitted form. */
export function formValues(form: HTMLFormElement): Record<string, string> {
  const values: Record<string, string> = {};
  new FormData(form).forEach((value, key) => {
    if (typeof value === "string") values[key] = value;
  });
  return values;
}

/** Moves focus to the first field with an error, in document order (WCAG 3.3.1). */
export function focusFirstInvalid(form: HTMLFormElement, fields: readonly string[]): void {
  for (const element of Array.from(form.elements)) {
    if (
      (element instanceof HTMLInputElement || element instanceof HTMLSelectElement) &&
      fields.includes(element.name)
    ) {
      element.focus();
      return;
    }
  }
}
