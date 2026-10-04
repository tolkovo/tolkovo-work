import type { TFunction } from "i18next";

const knownErrors = ["token_revoked", "session_revoked", "banned"] as const;

/** What broke and what the admin does about it (Channel.error_code in api/openapi.yaml). */
export function channelError(t: TFunction, code: string | null): string {
  const known = knownErrors.find((k) => k === code);
  return t(`settings.channels.errorCode.${known ?? "other"}`);
}
