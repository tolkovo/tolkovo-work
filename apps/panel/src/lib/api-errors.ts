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
    case "registration_closed":
      return { message: t("errors.registrationClosed") };
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
    case "protected_member":
      return { message: t("errors.protectedMember") };
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
    case "github_not_connected":
      return { message: t("errors.githubNotConnected") };
    case "issue_in_progress":
      return { message: t("errors.issueInProgress") };
    case "issue_linked":
      return { message: t("errors.issueLinked") };
    case "issue_not_found":
      return { message: t("errors.issueNotFound") };
    case "issue_state":
      return { message: t("errors.issueState") };
    case "task_conflict":
      return { message: t("errors.taskConflict") };
    case "task_linked":
      return { message: t("errors.taskLinked") };
    case "queue_exists":
      return { message: t("errors.queueExists"), ...(field ? { field } : {}) };
    case "topic_exists":
      return { message: t("errors.topicExists"), ...(field ? { field } : {}) };
    case "tag_exists":
      return { message: t("errors.tagExists"), ...(field ? { field } : {}) };
    case "tasks_disabled":
      return { message: t("errors.tasksDisabled") };
    case "no_notify_bot":
      return { message: t("errors.noNotifyBot") };
    case "notify_bot_busy":
      return { message: t("errors.notifyBotBusy") };
    case "notify_bot_revoked":
      return { message: t("errors.notifyBotRevoked") };
    case "file_rejected":
      return { message: t("errors.fileRejected") };
    case "scan_unavailable":
      return { message: t("errors.scanUnavailable") };
    case "too_many_tokens":
      return { message: t("errors.tooManyTokens") };
    case "invalid_access_token":
      return { message: t("errors.invalidAccessToken") };
    case "ai_not_configured":
      return { message: t("errors.aiNotConfigured") };
    case "ai_budget_exceeded":
      return { message: t("errors.aiBudgetExceeded") };
    case "ai_key_required":
      return { message: t("errors.aiKeyRequired"), ...(field ? { field } : {}) };
    case "ai_key_refused":
      return { message: t("errors.aiKeyRefused") };
    case "ai_request_rejected":
      return { message: t("errors.aiRequestRejected") };
    case "ai_rate_limited":
      return { message: t("errors.aiRateLimited") };
    case "ai_provider_unavailable":
      return { message: t("errors.aiProviderUnavailable") };
    case "ai_bad_answer":
      return { message: t("errors.aiBadAnswer") };
    default:
      return { message: t("errors.unknown") };
  }
}
