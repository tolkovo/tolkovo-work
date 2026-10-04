import { isApiError } from "@tolkovo/api-client";
import { useResetPassword } from "@tolkovo/api-client/react";
import { createFileRoute, Link } from "@tanstack/react-router";
import { useState } from "react";
import { useTranslation } from "react-i18next";

import { AuthCard } from "../../components/auth/auth-card";
import { MessageCard } from "../../components/auth/message-card";
import { FormMessage, TextField } from "../../components/form/fields";
import { describeApiError } from "../../lib/api-errors";
import { focusFirstInvalid, formValues, resetPasswordSchema, validateForm, type FieldErrors } from "../../lib/forms";
import { useLinkToken } from "../../lib/link-token";

export const Route = createFileRoute("/_link/reset-password")({
  component: ResetPasswordPage,
});

type Field = "password" | "confirm_password";

const linkClass = "font-medium text-teal underline-offset-4 hover:underline";

/** Sets a new password by the emailed link. Every session ends; the person signs in again. */
function ResetPasswordPage() {
  const { t } = useTranslation();
  const token = useLinkToken();
  const reset = useResetPassword();
  const [fieldErrors, setFieldErrors] = useState<FieldErrors<Field>>({});

  if (!token) {
    return (
      <MessageCard title={t("auth.reset.title")}>
        <p className="m-0">{t("auth.link.missing")}</p>
        <Link to="/forgot-password" className={linkClass}>
          {t("auth.reset.requestNew")}
        </Link>
      </MessageCard>
    );
  }

  const onSubmit = (form: HTMLFormElement) => {
    const result = validateForm(resetPasswordSchema, formValues(form));
    if (!result.ok) {
      setFieldErrors(result.errors);
      focusFirstInvalid(form, Object.keys(result.errors));
      return;
    }
    setFieldErrors({});
    reset.mutate({ token, password: result.data.password });
  };

  const fieldError = (field: Field) => {
    const key = fieldErrors[field];
    return key ? t(`validation.${key}`) : undefined;
  };
  const invalidLink = isApiError(reset.error) && reset.error.code === "invalid_token";

  return (
    <AuthCard
      title={t("auth.reset.title")}
      subtitle={t("auth.reset.subtitle")}
      submitLabel={t("auth.reset.submit")}
      pendingLabel={t("auth.reset.pending")}
      pending={reset.isPending}
      error={reset.isError ? describeApiError(t, reset.error).message : null}
      onSubmit={onSubmit}
      // A used or expired link cannot be fixed by retyping: offer a new one.
      {...(invalidLink ? { switchAction: t("auth.reset.requestNew"), switchTo: "/forgot-password" as const } : {})}
      done={
        reset.isSuccess ? (
          <>
            <FormMessage tone="success" message={t("auth.reset.done")} />
            <Link to="/login" className={linkClass}>
              {t("auth.link.toLogin")}
            </Link>
          </>
        ) : undefined
      }
    >
      <TextField
        id="reset-password"
        name="password"
        type="password"
        label={t("auth.reset.password")}
        hint={t("settings.security.password.newHint")}
        autoComplete="new-password"
        required
        error={fieldError("password")}
      />
      <TextField
        id="reset-confirm"
        name="confirm_password"
        type="password"
        label={t("auth.reset.confirm")}
        autoComplete="new-password"
        required
        error={fieldError("confirm_password")}
      />
    </AuthCard>
  );
}
