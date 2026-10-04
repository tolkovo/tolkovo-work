import { isApiError } from "@tolkovo/api-client";
import { useVerifyEmail } from "@tolkovo/api-client/react";
import { createFileRoute, Link } from "@tanstack/react-router";
import { useEffect, useRef } from "react";
import { useTranslation } from "react-i18next";

import { MessageCard } from "../../components/auth/message-card";
import { FormMessage } from "../../components/form/fields";
import { describeApiError } from "../../lib/api-errors";
import { useLinkToken } from "../../lib/link-token";
import { HOME_PATH } from "../../lib/redirect";

export const Route = createFileRoute("/_link/verify-email")({
  component: VerifyEmailPage,
});

const linkClass = "font-medium text-teal underline-offset-4 hover:underline";

/** Confirms the email as soon as the link opens; no session is needed. */
function VerifyEmailPage() {
  const { t } = useTranslation();
  const token = useLinkToken();
  const verify = useVerifyEmail();
  const sent = useRef(false);

  useEffect(() => {
    // Once: the token works a single time, and StrictMode runs effects twice in development.
    if (!token || sent.current) return;
    sent.current = true;
    verify.mutate(token);
  }, [token, verify]);

  const message = !token
    ? t("auth.link.missing")
    : verify.isError
      ? isApiError(verify.error) && verify.error.code === "invalid_token"
        ? t("auth.verify.invalid")
        : describeApiError(t, verify.error).message
      : null;

  return (
    <MessageCard title={t("auth.verify.title")}>
      {verify.isSuccess ? (
        <FormMessage tone="success" message={t("auth.verify.done")} />
      ) : message ? (
        <FormMessage tone="error" message={message} />
      ) : (
        <p role="status" className="m-0">
          {t("auth.verify.pending")}
        </p>
      )}
      <Link to={HOME_PATH} className={linkClass}>
        {t("auth.verify.continue")}
      </Link>
    </MessageCard>
  );
}
