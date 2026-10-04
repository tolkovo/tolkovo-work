import { createFileRoute } from "@tanstack/react-router";
import { Input, Label } from "@tolkovo/ui";
import { useTranslation } from "react-i18next";

import { AuthCard, Field } from "../../components/auth/auth-card";

export const Route = createFileRoute("/_auth/register")({
  component: RegisterPage,
});

function RegisterPage() {
  const { t } = useTranslation();
  return (
    <AuthCard
      title={t("auth.register.title")}
      subtitle={t("auth.register.subtitle")}
      submitLabel={t("auth.register.submit")}
      switchPrompt={t("auth.register.switchPrompt")}
      switchAction={t("auth.register.switchAction")}
      switchTo="/login"
    >
      <Field>
        <Label htmlFor="register-name">{t("auth.register.name")}</Label>
        <Input id="register-name" name="name" autoComplete="name" required />
      </Field>
      <Field>
        <Label htmlFor="register-workspace">{t("auth.register.workspace")}</Label>
        <Input id="register-workspace" name="workspace" autoComplete="organization" required />
      </Field>
      <Field>
        <Label htmlFor="register-email">{t("auth.email")}</Label>
        <Input id="register-email" name="email" type="email" autoComplete="email" inputMode="email" required />
      </Field>
      <Field>
        <Label htmlFor="register-password">{t("auth.password")}</Label>
        <Input id="register-password" name="password" type="password" autoComplete="new-password" required />
      </Field>
    </AuthCard>
  );
}
