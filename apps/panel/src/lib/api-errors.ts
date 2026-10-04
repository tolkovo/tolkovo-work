import { isApiError } from "@tolkovo/api-client";
import type { TFunction } from "i18next";

/**
 * Turns a failed API call into words for people (docs/07, docs/00 §7.4): what happened and
 * what to do. Server `detail` texts are English and technical, so they are never shown; the
 * stable `code` picks the message. Some codes belong to a form field (`field`).
 */
export type DescribedError<F extends string = string> = { message: string; field?: F };

export function describeApiError<F extends string = string>(
  t: TFunction,
  error: unknown,
  fields: Partial<Record<string, F>> = {},
): DescribedError<F> {
  if (!isApiError(error)) return { message: t("errors.unknown") };
  const field = fields[error.code];
  switch (error.code) {
    case "network":
      return { message: t("errors.network") };
    case "too_many_requests":
      return {
        message:
          error.retryAfter !== null
            ? t("errors.rateLimited", { seconds: error.retryAfter })
            : t("errors.rateLimitedLater"),
      };
    case "invalid_credentials":
      return { message: t("errors.invalidCredentials") };
    case "email_taken":
      return { message: t("errors.emailTaken"), ...(field ? { field } : {}) };
    case "slug_taken":
      return { message: t("errors.slugTaken"), ...(field ? { field } : {}) };
    case "invalid_request":
      return { message: t("errors.invalidRequest") };
    case "unauthenticated":
      return { message: t("errors.unauthenticated") };
    case "forbidden":
      return { message: t("errors.forbidden") };
    case "csrf_failed":
    case "cross_origin":
      return { message: t("errors.staleSession") };
    case "no_workspace":
      return { message: t("errors.noWorkspace") };
    case "not_found":
      return { message: t("errors.notFound") };
    default:
      return { message: t("errors.unknown") };
  }
}
