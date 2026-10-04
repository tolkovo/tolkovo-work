import { isApiError, type InvitationPreview } from "@tolkovo/api-client";
import { useAcceptInvitation, useInvitationPreview } from "@tolkovo/api-client/react";
import { isLanguage } from "@tolkovo/i18n";
import { Button } from "@tolkovo/ui";
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { useTranslation } from "react-i18next";

import { AuthCard } from "../../components/auth/auth-card";
import { MessageCard } from "../../components/auth/message-card";
import { FormMessage, TextField } from "../../components/form/fields";
import { describeApiError } from "../../lib/api-errors";
import {
  acceptExistingAccountSchema,
  acceptNewAccountSchema,
  focusFirstInvalid,
  formValues,
  validateForm,
  type FieldErrors,
} from "../../lib/forms";
import { useLinkToken } from "../../lib/link-token";
import { HOME_PATH } from "../../lib/redirect";

export const Route = createFileRoute("/_link/invite")({
  component: InvitePage,
});

const linkClass = "font-medium text-teal underline-offset-4 hover:underline";

/**
 * Accepts an invitation (docs/06 A-03). Without an account the person picks a name and a
 * password; with one they enter its password and the workspace joins their others.
 */
function InvitePage() {
  const { t } = useTranslation();
  const token = useLinkToken();
  const preview = useInvitationPreview(token);

  if (!token) {
    return (
      <MessageCard title={t("auth.invite.title")}>
        <p className="m-0">{t("auth.link.missing")}</p>
      </MessageCard>
    );
  }
  if (preview.isPending) {
    return (
      <MessageCard title={t("auth.invite.title")}>
        <p role="status" className="m-0">
          {t("auth.invite.loading")}
        </p>
      </MessageCard>
    );
  }
  if (preview.isError) {
    const invalid = isApiError(preview.error) && preview.error.code === "invalid_token";
    return (
      <MessageCard title={t("auth.invite.title")}>
        <FormMessage
          tone="error"
          message={invalid ? t("auth.invite.invalid") : describeApiError(t, preview.error).message}
        />
        {invalid ? (
          <Link to="/login" className={linkClass}>
            {t("auth.link.toLogin")}
          </Link>
        ) : (
          <Button variant="secondary" onClick={() => void preview.refetch()}>
            {t("error.retry")}
          </Button>
        )}
      </MessageCard>
    );
  }
  return <AcceptForm token={token} invitation={preview.data} />;
}

type Field = "name" | "password";

function AcceptForm({ token, invitation }: { token: string; invitation: InvitationPreview }) {
  const { t, i18n } = useTranslation();
  const navigate = useNavigate();
  const accept = useAcceptInvitation();
  const [fieldErrors, setFieldErrors] = useState<FieldErrors<Field>>({});
  const existing = invitation.account_exists;

  const onSubmit = (form: HTMLFormElement) => {
    const values = formValues(form);
    const result = existing
      ? validateForm(acceptExistingAccountSchema, values)
      : validateForm(acceptNewAccountSchema, values);
    if (!result.ok) {
      setFieldErrors(result.errors);
      focusFirstInvalid(form, Object.keys(result.errors));
      return;
    }
    setFieldErrors({});
    const locale = isLanguage(i18n.resolvedLanguage) ? i18n.resolvedLanguage : undefined;
    accept.mutate(
      { token, ...result.data, ...(!existing && locale ? { locale } : {}) },
      { onSuccess: () => void navigate({ href: HOME_PATH, replace: true }) },
    );
  };

  const fieldError = (field: Field) => {
    const key = fieldErrors[field];
    return key ? t(`validation.${key}`, { max: field === "name" ? 120 : 128 }) : undefined;
  };

  return (
    <AuthCard
      title={t("auth.invite.title")}
      subtitle={t("auth.invite.subtitle", { workspace: invitation.workspace_name, role: t(`roles.${invitation.role}`) })}
      submitLabel={t("auth.invite.submit")}
      pendingLabel={t("auth.invite.pending")}
      pending={accept.isPending}
      error={accept.isError ? describeApiError(t, accept.error).message : null}
      onSubmit={onSubmit}
      {...(existing ? { switchAction: t("auth.invite.forgot"), switchTo: "/forgot-password" as const } : {})}
    >
      {existing ? <p className="m-0 text-[13px] text-ink-2">{t("auth.invite.existing")}</p> : null}
      {/* Read-only, not submitted: it tells password managers which account this is. */}
      <TextField
        id="invite-email"
        name="invited_email"
        type="email"
        label={t("auth.invite.email")}
        autoComplete="username"
        value={invitation.email}
        readOnly
      />
      {existing ? null : (
        <TextField
          id="invite-name"
          name="name"
          label={t("auth.invite.name")}
          hint={t("auth.invite.nameHint")}
          autoComplete="name"
          required
          error={fieldError("name")}
        />
      )}
      <TextField
        id="invite-password"
        name="password"
        type="password"
        label={existing ? t("auth.password") : t("auth.invite.newPassword")}
        {...(existing ? {} : { hint: t("auth.register.passwordHint") })}
        autoComplete={existing ? "current-password" : "new-password"}
        required
        error={fieldError("password")}
      />
    </AuthCard>
  );
}
