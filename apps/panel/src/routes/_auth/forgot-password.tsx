import { useForgotPassword } from "@tolkovo/api-client/react";
import { createFileRoute } from "@tanstack/react-router";
import { useState } from "react";
import { useTranslation } from "react-i18next";

import { AuthCard } from "../../components/auth/auth-card";
import { FormMessage, TextField } from "../../components/form/fields";
import { describeApiError } from "../../lib/api-errors";
import { focusFirstInvalid, formValues, forgotPasswordSchema, validateForm, type FieldErrors } from "../../lib/forms";

export const Route = createFileRoute("/_auth/forgot-password")({
  component: ForgotPasswordPage,
});

/**
 * Asks for a reset link. The answer is the same whether or not the address has an account,
 * so the page cannot be used to find out who is registered.
 */
function ForgotPasswordPage() {
  const { t } = useTranslation();
  const forgot = useForgotPassword();
  const [fieldErrors, setFieldErrors] = useState<FieldErrors<"email">>({});

  const onSubmit = (form: HTMLFormElement) => {
    const result = validateForm(forgotPasswordSchema, formValues(form));
    if (!result.ok) {
      setFieldErrors(result.errors);
      focusFirstInvalid(form, Object.keys(result.errors));
      return;
    }
    setFieldErrors({});
    forgot.mutate(result.data.email);
  };

  const emailError = fieldErrors.email ? t(`validation.${fieldErrors.email}`, { max: 254 }) : undefined;

  return (
    <AuthCard
      title={t("auth.forgot.title")}
      subtitle={t("auth.forgot.subtitle")}
      submitLabel={t("auth.forgot.submit")}
      pendingLabel={t("auth.forgot.pending")}
      pending={forgot.isPending}
      error={forgot.isError ? describeApiError(t, forgot.error).message : null}
      onSubmit={onSubmit}
      switchPrompt={t("auth.forgot.switchPrompt")}
      switchAction={t("auth.forgot.switchAction")}
      switchTo="/login"
      done={forgot.isSuccess ? <FormMessage tone="success" message={t("auth.forgot.sent")} /> : undefined}
    >
      <TextField
        id="forgot-email"
        name="email"
        type="email"
        label={t("auth.email")}
        autoComplete="username"
        inputMode="email"
        required
        error={emailError}
      />
    </AuthCard>
  );
}
