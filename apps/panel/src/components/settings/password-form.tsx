import { useChangePassword } from "@tolkovo/api-client/react";
import { Button } from "@tolkovo/ui";
import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

import { describeApiError } from "../../lib/api-errors";
import { focusFirstInvalid, formValues, passwordSchema, validateForm, type FieldErrors } from "../../lib/forms";
import { FormMessage, TextField } from "../form/fields";

type Field = "current_password" | "new_password" | "confirm_password";

/**
 * POST /me/password. On success the server signs out every other device and rotates this
 * session; the hook stores the new session (new CSRF token), so the panel keeps working.
 */
export function PasswordForm() {
  const { t } = useTranslation();
  const change = useChangePassword();
  const [fieldErrors, setFieldErrors] = useState<FieldErrors<Field>>({});
  const [serverField, setServerField] = useState<Field | null>(null);
  const [done, setDone] = useState(false);

  const onSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const form = event.currentTarget;
    setDone(false);
    setServerField(null);
    const result = validateForm(passwordSchema, formValues(form));
    if (!result.ok) {
      setFieldErrors(result.errors);
      focusFirstInvalid(form, Object.keys(result.errors));
      return;
    }
    setFieldErrors({});
    const { current_password, new_password } = result.data;
    change.mutate(
      { current_password, new_password },
      {
        onSuccess: () => {
          setDone(true);
          form.reset();
        },
        onError: (error) => {
          const { field } = describeApiError<Field>(t, error, { wrong_password: "current_password" });
          if (field) {
            setServerField(field);
            // After the re-render that enables the fieldset again: a disabled field takes no focus.
            setTimeout(() => focusFirstInvalid(form, [field]), 0);
          }
        },
      },
    );
  };

  const described = change.isError
    ? describeApiError<Field>(t, change.error, { wrong_password: "current_password" })
    : null;
  const fieldError = (field: Field) => {
    if (serverField === field && described) return described.message;
    const key = fieldErrors[field];
    return key ? t(`validation.${key}`, { max: 128 }) : undefined;
  };

  return (
    <form
      onSubmit={onSubmit}
      noValidate
      aria-busy={change.isPending}
      aria-labelledby="settings-password-title"
      className="flex max-w-[480px] flex-col gap-4"
    >
      <h3 id="settings-password-title" className="m-0 text-sm font-semibold">
        {t("settings.security.password.title")}
      </h3>
      <p className="m-0 -mt-2 text-[13px] text-ink-2">{t("settings.security.password.text")}</p>
      {/* A field-bound error is shown at its field; everything else here. */}
      <FormMessage tone="error" message={described && !described.field ? described.message : null} />
      <fieldset disabled={change.isPending} className="m-0 flex flex-col gap-4 border-0 p-0">
        <TextField
          id="password-current"
          name="current_password"
          type="password"
          label={t("settings.security.password.current")}
          autoComplete="current-password"
          required
          error={fieldError("current_password")}
        />
        <TextField
          id="password-new"
          name="new_password"
          type="password"
          label={t("settings.security.password.new")}
          hint={t("settings.security.password.newHint")}
          autoComplete="new-password"
          required
          error={fieldError("new_password")}
        />
        <TextField
          id="password-confirm"
          name="confirm_password"
          type="password"
          label={t("settings.security.password.confirm")}
          autoComplete="new-password"
          required
          error={fieldError("confirm_password")}
        />
      </fieldset>
      <div className="flex flex-wrap items-center gap-3">
        <Button type="submit" variant="primary" size="lg" disabled={change.isPending}>
          {change.isPending ? t("settings.security.password.pending") : t("settings.security.password.submit")}
        </Button>
        <FormMessage tone="success" message={done ? t("settings.security.password.done") : null} />
      </div>
    </form>
  );
}
