import { useLogin, useRegistrationOpen } from "@tolkovo/api-client/react";
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { useTranslation } from "react-i18next";

import { AuthCard } from "../../components/auth/auth-card";
import { TextField } from "../../components/form/fields";
import { describeApiError } from "../../lib/api-errors";
import { focusFirstInvalid, formValues, loginSchema, validateForm, type FieldErrors } from "../../lib/forms";
import { HOME_PATH, safeReturnPath, validateLoginSearch } from "../../lib/redirect";

export const Route = createFileRoute("/_auth/login")({
  validateSearch: validateLoginSearch,
  component: LoginPage,
});

type Field = "email" | "password";

function LoginPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { redirect } = Route.useSearch();
  const login = useLogin();
  // "Create workspace" only where the server takes new accounts (self-host: the first one).
  const registrationOpen = useRegistrationOpen().data === true;
  const [fieldErrors, setFieldErrors] = useState<FieldErrors<Field>>({});

  const onSubmit = (form: HTMLFormElement) => {
    const result = validateForm(loginSchema, formValues(form));
    if (!result.ok) {
      setFieldErrors(result.errors);
      focusFirstInvalid(form, Object.keys(result.errors));
      return;
    }
    setFieldErrors({});
    login.mutate(result.data, {
      // Checked again right before use: navigate({ href }) leaves the SPA for any URL scheme.
      onSuccess: () => void navigate({ href: safeReturnPath(redirect) ?? HOME_PATH, replace: true }),
    });
  };

  const fieldError = (field: Field) => {
    const key = fieldErrors[field];
    return key ? t(`validation.${key}`, { max: field === "email" ? 254 : 128 }) : undefined;
  };

  return (
    <AuthCard
      title={t("auth.login.title")}
      subtitle={t("auth.login.subtitle")}
      submitLabel={t("auth.login.submit")}
      pendingLabel={t("auth.login.pending")}
      pending={login.isPending}
      error={login.isError ? describeApiError(t, login.error).message : null}
      onSubmit={onSubmit}
      {...(registrationOpen
        ? { switchPrompt: t("auth.login.switchPrompt"), switchAction: t("auth.login.switchAction"), switchTo: "/register" as const }
        : {})}
    >
      <TextField
        id="login-email"
        name="email"
        type="email"
        label={t("auth.email")}
        autoComplete="username"
        inputMode="email"
        required
        error={fieldError("email")}
      />
      <TextField
        id="login-password"
        name="password"
        type="password"
        label={t("auth.password")}
        autoComplete="current-password"
        required
        error={fieldError("password")}
      />
      <Link
        to="/forgot-password"
        className="-mt-1.5 self-end text-[13px] font-medium text-teal underline-offset-4 hover:underline"
      >
        {t("auth.login.forgot")}
      </Link>
    </AuthCard>
  );
}
