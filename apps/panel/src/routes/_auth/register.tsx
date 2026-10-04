import { isLanguage } from "@tolkovo/i18n";
import { useRegister } from "@tolkovo/api-client/react";
import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { useTranslation } from "react-i18next";

import { AuthCard } from "../../components/auth/auth-card";
import { TextField } from "../../components/form/fields";
import { describeApiError } from "../../lib/api-errors";
import { focusFirstInvalid, formValues, registerSchema, validateForm, type FieldErrors } from "../../lib/forms";
import { slugify } from "../../lib/slug";
import { browserTimeZone } from "../../lib/timezones";

export const Route = createFileRoute("/_auth/register")({
  component: RegisterPage,
});

type Field = "name" | "workspace_name" | "workspace_slug" | "email" | "password";

const maxLength: Record<Field, number> = {
  name: 120,
  workspace_name: 120,
  workspace_slug: 40,
  email: 254,
  password: 128,
};

function RegisterPage() {
  const { t, i18n } = useTranslation();
  const navigate = useNavigate();
  const register = useRegister();
  const [fieldErrors, setFieldErrors] = useState<FieldErrors<Field>>({});
  const [serverField, setServerField] = useState<{ field: Field; message: string } | null>(null);
  // The address follows the company name until the person edits it.
  const [slug, setSlug] = useState("");
  const [slugEdited, setSlugEdited] = useState(false);

  const onSubmit = (form: HTMLFormElement) => {
    const result = validateForm(registerSchema, formValues(form));
    setServerField(null);
    if (!result.ok) {
      setFieldErrors(result.errors);
      focusFirstInvalid(form, Object.keys(result.errors));
      return;
    }
    setFieldErrors({});
    const timezone = browserTimeZone();
    register.mutate(
      {
        ...result.data,
        locale: isLanguage(i18n.resolvedLanguage) ? i18n.resolvedLanguage : "ru",
        ...(timezone ? { timezone } : {}),
      },
      {
        onSuccess: () => void navigate({ to: "/onboarding", replace: true }),
        onError: (error) => {
          const described = describeApiError<Field>(t, error, {
            email_taken: "email",
            slug_taken: "workspace_slug",
          });
          if (described.field) {
            setServerField({ field: described.field, message: described.message });
            focusFirstInvalid(form, [described.field]);
          }
        },
      },
    );
  };

  const fieldError = (field: Field) => {
    if (serverField?.field === field) return serverField.message;
    const key = fieldErrors[field];
    return key ? t(`validation.${key}`, { max: maxLength[field] }) : undefined;
  };

  // A field-bound server error is shown at the field, not repeated at the top.
  const formError =
    register.isError && !serverField ? describeApiError(t, register.error).message : null;

  return (
    <AuthCard
      title={t("auth.register.title")}
      subtitle={t("auth.register.subtitle")}
      submitLabel={t("auth.register.submit")}
      pendingLabel={t("auth.register.pending")}
      pending={register.isPending}
      error={formError}
      onSubmit={onSubmit}
      switchPrompt={t("auth.register.switchPrompt")}
      switchAction={t("auth.register.switchAction")}
      switchTo="/login"
    >
      <TextField
        id="register-name"
        name="name"
        label={t("auth.register.name")}
        autoComplete="name"
        required
        error={fieldError("name")}
      />
      <TextField
        id="register-workspace"
        name="workspace_name"
        label={t("auth.register.workspace")}
        autoComplete="organization"
        required
        error={fieldError("workspace_name")}
        onChange={(event) => {
          if (!slugEdited) setSlug(slugify(event.currentTarget.value));
        }}
      />
      <TextField
        id="register-slug"
        name="workspace_slug"
        label={t("auth.register.slug")}
        hint={t("auth.register.slugHint")}
        autoComplete="off"
        autoCapitalize="none"
        spellCheck={false}
        required
        value={slug}
        onChange={(event) => {
          setSlugEdited(true);
          setSlug(event.currentTarget.value.toLowerCase());
        }}
        error={fieldError("workspace_slug")}
      />
      <TextField
        id="register-email"
        name="email"
        type="email"
        label={t("auth.email")}
        autoComplete="email"
        inputMode="email"
        required
        error={fieldError("email")}
      />
      <TextField
        id="register-password"
        name="password"
        type="password"
        label={t("auth.password")}
        hint={t("auth.register.passwordHint")}
        autoComplete="new-password"
        required
        error={fieldError("password")}
      />
    </AuthCard>
  );
}
