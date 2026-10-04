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
    case "wrong_password":
      return { message: t("errors.wrongPassword"), ...(field ? { field } : {}) };
    case "current_session":
      return { message: t("errors.currentSession") };
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
    case "invalid_token":
      return { message: t("errors.invalidToken") };
    case "email_disabled":
      return { message: t("errors.emailDisabled") };
    case "already_member":
      return { message: t("errors.alreadyMember"), ...(field ? { field } : {}) };
    case "email_verified":
      return { message: t("errors.emailVerified") };
    case "channel_exists":
      return { message: t("errors.channelExists"), ...(field ? { field } : {}) };
    case "telegram_token_invalid":
      return { message: t("errors.telegramTokenInvalid"), ...(field ? { field } : {}) };
    case "telegram_phone_invalid":
      return { message: t("errors.telegramPhoneInvalid"), ...(field ? { field } : {}) };
    case "telegram_no_account":
      return { message: t("errors.telegramNoAccount"), ...(field ? { field } : {}) };
    case "telegram_banned":
      return { message: t("errors.telegramBanned"), ...(field ? { field } : {}) };
    case "telegram_code_invalid":
      return { message: t("errors.telegramCodeInvalid"), ...(field ? { field } : {}) };
    case "telegram_code_expired":
      return { message: t("errors.telegramCodeExpired") };
    case "telegram_password_invalid":
      return { message: t("errors.telegramPasswordInvalid"), ...(field ? { field } : {}) };
    case "login_step":
      return { message: t("errors.loginStep") };
    case "login_expired":
      return { message: t("errors.loginExpired") };
    case "telegram_unavailable":
      return { message: t("errors.telegramUnavailable") };
    case "encryption_unavailable":
      return { message: t("errors.encryptionUnavailable") };
    case "telegram_user_unavailable":
      return { message: t("errors.telegramUserUnavailable") };
    case "github_connected":
      return { message: t("errors.githubConnected") };
    case "github_step":
      return { message: t("errors.githubStep") };
    case "github_state":
      return { message: t("errors.githubState") };
    case "github_code_expired":
      return { message: t("errors.githubCodeExpired") };
    case "github_installation_invalid":
      return { message: t("errors.githubInstallationInvalid") };
    case "github_repository_unreachable":
      return { message: t("errors.githubRepositoryUnreachable"), ...(field ? { field } : {}) };
    case "github_app_refused":
      return { message: t("errors.githubAppRefused") };
    case "github_unavailable":
      return { message: t("errors.githubUnavailable") };
    default:
      return { message: t("errors.unknown") };
  }
}
