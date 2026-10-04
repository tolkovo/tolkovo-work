import type { User } from "@tolkovo/api-client";
import { useUpdateMe } from "@tolkovo/api-client/react";
import { languageNames, languages } from "@tolkovo/i18n";
import { Button } from "@tolkovo/ui";
import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

import { describeApiError } from "../../lib/api-errors";
import { focusFirstInvalid, formValues, profileSchema, validateForm, type FieldErrors } from "../../lib/forms";
import { FormMessage, SelectField, TextField } from "../form/fields";

type Field = "name" | "locale";

/**
 * The signed-in user's name and interface language (PATCH /me). The language is saved on the
 * server and wins over the browser copy (docs/04 §8.2); the shell applies it when the session
 * changes, and localStorage keeps it for the sign-in screen.
 */
export function ProfileForm({ user }: { user: User }) {
  const { t, i18n } = useTranslation();
  const update = useUpdateMe();
  const [fieldErrors, setFieldErrors] = useState<FieldErrors<Field>>({});
  const [saved, setSaved] = useState(false);

  const onSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const form = event.currentTarget;
    setSaved(false);
    const result = validateForm(profileSchema, formValues(form));
    if (!result.ok) {
      setFieldErrors(result.errors);
      focusFirstInvalid(form, Object.keys(result.errors));
      return;
    }
    setFieldErrors({});
    update.mutate(result.data, {
      onSuccess: (session) => {
        setSaved(true);
        void i18n.changeLanguage(session.user.locale);
      },
    });
  };

  const fieldError = (field: Field) => {
    const key = fieldErrors[field];
    return key ? t(`validation.${key}`, { max: 120 }) : undefined;
  };

  return (
    <form onSubmit={onSubmit} noValidate aria-busy={update.isPending} className="flex max-w-[480px] flex-col gap-4">
      <FormMessage tone="error" message={update.isError ? describeApiError(t, update.error).message : null} />
      <fieldset disabled={update.isPending} className="m-0 flex flex-col gap-4 border-0 p-0">
        <TextField
          id="profile-name"
          name="name"
          label={t("settings.profile.name")}
          hint={t("settings.profile.nameHint")}
          defaultValue={user.name}
          autoComplete="name"
          maxLength={120}
          required
          error={fieldError("name")}
        />
        <TextField
          id="profile-email"
          name="email"
          type="email"
          label={t("settings.profile.email")}
          hint={t("settings.profile.emailHint")}
          defaultValue={user.email}
          readOnly
        />
        <SelectField
          id="profile-locale"
          name="locale"
          label={t("settings.profile.language")}
          hint={t("settings.profile.languageHint")}
          defaultValue={user.locale}
          error={fieldError("locale")}
        >
          {languages.map((lang) => (
            <option key={lang} value={lang} lang={lang}>
              {languageNames[lang]}
            </option>
          ))}
        </SelectField>
      </fieldset>
      <div className="flex items-center gap-3">
        <Button type="submit" variant="primary" size="lg" disabled={update.isPending}>
          {update.isPending ? t("settings.profile.saving") : t("settings.profile.save")}
        </Button>
        <FormMessage tone="success" message={saved ? t("settings.profile.saved") : null} />
      </div>
    </form>
  );
}
