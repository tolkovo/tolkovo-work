import type { UpdateWorkspaceRequest, Workspace } from "@tolkovo/api-client";
import { useCan, useUpdateWorkspace } from "@tolkovo/api-client/react";
import { languageNames, languages } from "@tolkovo/i18n";
import { Button } from "@tolkovo/ui";
import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

import { describeApiError } from "../../lib/api-errors";
import { focusFirstInvalid, formValues, validateForm, workspaceSchema, type FieldErrors } from "../../lib/forms";
import { timeZoneOptions } from "../../lib/timezones";
import { FormMessage, SelectField, TextField } from "../form/fields";

type Field = "name" | "locale" | "timezone";

type WorkspaceFormProps = {
  workspace: Workspace;
  /** Which fields to show: onboarding asks only for language and time zone. */
  fields?: readonly Field[];
  submitLabel?: string;
  onSaved?: (workspace: Workspace) => void;
};

/**
 * Workspace settings (PATCH /workspace). Without `workspace.manage` the form is read-only and
 * says who can change it; the server enforces the permission regardless (docs/00 §9.2).
 */
export function WorkspaceForm({ workspace, fields = ["name", "locale", "timezone"], submitLabel, onSaved }: WorkspaceFormProps) {
  const { t } = useTranslation();
  const canManage = useCan("workspace.manage");
  const update = useUpdateWorkspace();
  const [fieldErrors, setFieldErrors] = useState<FieldErrors<Field>>({});
  const [saved, setSaved] = useState(false);

  const onSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const form = event.currentTarget;
    setSaved(false);
    const result = validateForm(workspaceSchema, { ...workspace, ...formValues(form) });
    if (!result.ok) {
      setFieldErrors(result.errors);
      focusFirstInvalid(form, Object.keys(result.errors));
      return;
    }
    setFieldErrors({});
    // Send only the fields this form shows: a partial update never resets the others.
    const { name, locale, timezone } = result.data;
    const body: UpdateWorkspaceRequest = {
      ...(fields.includes("name") ? { name } : {}),
      ...(fields.includes("locale") ? { locale } : {}),
      ...(fields.includes("timezone") ? { timezone } : {}),
    };
    update.mutate(body, {
      onSuccess: (ws) => {
        setSaved(true);
        onSaved?.(ws);
      },
    });
  };

  const fieldError = (field: Field) => {
    const key = fieldErrors[field];
    return key ? t(`validation.${key}`, { max: field === "timezone" ? 64 : 120 }) : undefined;
  };

  return (
    <form onSubmit={onSubmit} noValidate aria-busy={update.isPending} className="flex max-w-[480px] flex-col gap-4">
      {!canManage ? <p className="m-0 text-[13px] text-ink-2">{t("settings.general.readOnly")}</p> : null}
      <FormMessage tone="error" message={update.isError ? describeApiError(t, update.error).message : null} />
      <fieldset disabled={!canManage || update.isPending} className="m-0 flex flex-col gap-4 border-0 p-0">
        {fields.includes("name") ? (
          <TextField
            id="workspace-name"
            name="name"
            label={t("settings.general.name")}
            defaultValue={workspace.name}
            autoComplete="organization"
            required
            error={fieldError("name")}
          />
        ) : null}
        {fields.includes("locale") ? (
          <SelectField
            id="workspace-locale"
            name="locale"
            label={t("settings.general.locale")}
            hint={t("settings.general.localeHint")}
            defaultValue={workspace.locale}
            error={fieldError("locale")}
          >
            {languages.map((lang) => (
              <option key={lang} value={lang} lang={lang}>
                {languageNames[lang]}
              </option>
            ))}
          </SelectField>
        ) : null}
        {fields.includes("timezone") ? (
          <SelectField
            id="workspace-timezone"
            name="timezone"
            label={t("settings.general.timezone")}
            hint={t("settings.general.timezoneHint")}
            defaultValue={workspace.timezone}
            error={fieldError("timezone")}
          >
            {timeZoneOptions(workspace.timezone).map((zone) => (
              <option key={zone} value={zone}>
                {zone.replaceAll("_", " ")}
              </option>
            ))}
          </SelectField>
        ) : null}
      </fieldset>
      {canManage ? (
        <div className="flex items-center gap-3">
          <Button type="submit" variant="primary" size="lg" disabled={update.isPending}>
            {update.isPending ? t("settings.general.saving") : (submitLabel ?? t("settings.general.save"))}
          </Button>
          <FormMessage tone="success" message={saved ? t("settings.general.saved") : null} />
        </div>
      ) : null}
    </form>
  );
}
