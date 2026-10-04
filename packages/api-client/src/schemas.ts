import { z } from "zod";

import type { components } from "./schema.gen";

/**
 * Runtime checks of API answers at the boundary (docs/00 §7.1). Each schema is tied to the
 * generated type with `satisfies`: when api/openapi.yaml adds a required field or changes a
 * type, `make gen` regenerates the type and these lines stop compiling until they match.
 * Unknown fields are stripped, so additive server changes never break the panel.
 */
type Schemas = components["schemas"];

export const localeSchema = z.enum(["ru", "en"]) satisfies z.ZodType<Schemas["Locale"]>;
export const roleSchema = z.enum(["owner", "admin", "operator"]) satisfies z.ZodType<Schemas["Role"]>;

export const problemSchema = z.object({
  type: z.string(),
  title: z.string(),
  status: z.number(),
  code: z.string(),
  detail: z.string().optional(),
  instance: z.string().optional(),
  request_id: z.string().optional(),
}) satisfies z.ZodType<Schemas["Problem"]>;

export const userSchema = z.object({
  id: z.string(),
  email: z.string(),
  name: z.string(),
  locale: localeSchema,
}) satisfies z.ZodType<Schemas["User"]>;

export const workspaceSchema = z.object({
  id: z.string(),
  slug: z.string(),
  name: z.string(),
  locale: localeSchema,
  timezone: z.string(),
}) satisfies z.ZodType<Schemas["Workspace"]>;

export const sessionSchema = z.object({
  user: userSchema,
  workspace: workspaceSchema,
  role: roleSchema,
  permissions: z.array(z.string()),
  csrf_token: z.string().min(1),
}) satisfies z.ZodType<Schemas["Session"]>;

export const workspaceListSchema = z.object({
  items: z.array(
    z.object({
      id: z.string(),
      slug: z.string(),
      name: z.string(),
      role: roleSchema,
    }) satisfies z.ZodType<Schemas["WorkspaceSummary"]>,
  ),
}) satisfies z.ZodType<Schemas["WorkspaceList"]>;

export const memberListSchema = z.object({
  items: z.array(
    z.object({
      user_id: z.string(),
      email: z.string(),
      name: z.string(),
      role: roleSchema,
      active: z.boolean(),
    }) satisfies z.ZodType<Schemas["Member"]>,
  ),
}) satisfies z.ZodType<Schemas["MemberList"]>;
