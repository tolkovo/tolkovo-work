import type {
  AcceptInvitationRequest,
  ChangePasswordRequest,
  ContactInput,
  CreateInvitationRequest,
  LoginRequest,
  RegisterRequest,
  ResetPasswordRequest,
  UpdateMeRequest,
  UpdateWorkspaceRequest,
} from "@tolkovo/api-client";
import { languages } from "@tolkovo/i18n";
import { z } from "zod";

/**
 * Client-side checks that mirror the constraints of api/openapi.yaml, so people see mistakes
 * before a round trip. The server validates again. Messages are keys of `validation.*`.
 * `satisfies` ties every form to the generated request type.
 */
export const validationKeys = ["required", "email", "passwordLength", "passwordMismatch", "slug", "tooLong", "phone"] as const;
export type ValidationKey = (typeof validationKeys)[number];

const required = (max: number) => z.string().trim().min(1, "required").max(max, "tooLong");

const EMAIL_PATTERN = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

const email = z
  .string()
  .trim()
  .min(1, "required")
  .max(254, "tooLong")
  // The server is the authority on addresses; this only catches obvious typos.
  .regex(EMAIL_PATTERN, "email");

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

export const profileSchema = z.object({
  name: required(120),
  locale: z.enum(languages),
}) satisfies z.ZodType<UpdateMeRequest>;

const newPassword = z.string().min(1, "required").min(8, "passwordLength").max(128, "passwordLength");

/** The confirmation never leaves the browser; it only catches a typo in the new password. */
export const passwordSchema = z
  .object({
    current_password: z.string().min(1, "required").max(128, "tooLong"),
    new_password: newPassword,
    confirm_password: z.string().min(1, "required"),
  })
  .refine((v) => v.new_password === v.confirm_password, { path: ["confirm_password"], message: "passwordMismatch" }) satisfies z.ZodType<
  ChangePasswordRequest & { confirm_password: string }
>;

export const forgotPasswordSchema = z.object({ email });

/** A new password typed twice; the confirmation never leaves the browser. */
export const resetPasswordSchema = z
  .object({ password: newPassword, confirm_password: z.string().min(1, "required") })
  .refine((v) => v.password === v.confirm_password, { path: ["confirm_password"], message: "passwordMismatch" }) satisfies z.ZodType<
  Omit<ResetPasswordRequest, "token"> & { confirm_password: string }
>;

/** Accepting an invitation without an account: a name and a new password. */
export const acceptNewAccountSchema = z.object({
  name: required(120),
  password: newPassword,
}) satisfies z.ZodType<Omit<AcceptInvitationRequest, "token" | "locale">>;

/** Accepting with an existing account: its current password, any length the server accepts. */
export const acceptExistingAccountSchema = z.object({
  password: z.string().min(1, "required").max(128, "tooLong"),
}) satisfies z.ZodType<Omit<AcceptInvitationRequest, "token" | "locale" | "name">>;

export const inviteSchema = z.object({
  email,
  role: z.enum(["operator", "programmer", "admin"]),
}) satisfies z.ZodType<CreateInvitationRequest>;

/** Contact fields (ContactInput); an empty email, phone or language clears it. */
export const contactSchema = z.object({
  name: z.string().trim().max(200, "tooLong"),
  email: z.string().trim().max(254, "tooLong").refine((v) => v === "" || EMAIL_PATTERN.test(v), "email"),
  // The same shape the server accepts (internal/conversations/contacts.go).
  phone: z
    .string()
    .trim()
    .refine((v) => v === "" || /^\+?[0-9][0-9 ()-]{1,30}$/.test(v), "phone"),
  locale: z.enum(["", "ru", "en"]),
}) satisfies z.ZodType<ContactInput>;

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
